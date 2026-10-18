package rules

import (
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/analyzer"
	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/detector"
	"github.com/dhyaneshsiddhartha15/DeployIQ/internal/dockerfile"
	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/constants"
)

// DefaultJavaVersion is used when neither the build file nor the existing
// Dockerfile names one: the current LTS most libraries support.
const DefaultJavaVersion = 21

const defaultJavaPort = "8080"

// jarPick selects the runnable jar from Maven's target/ or Gradle's
// build/libs/: not the sources, javadoc or Spring Boot "plain" jars, and not
// the shade/repackage plugins' original-*.jar.
const jarPick = `find %s -maxdepth 1 -name '*.jar' ! -name '*-sources.jar' ! -name '*-javadoc.jar' ! -name '*-plain.jar' ! -name 'original-*' | head -n 1`

type javaGenerator struct{}

func (javaGenerator) Stack() string { return constants.StackJava }

func (javaGenerator) Generate(ctx *Context) (*Result, error) {
	j := ctx.Detection.Java
	switch {
	case j.MultiModule:
		return nil, unsupported("this is a multi-module %s build; which module is the service cannot be inferred yet. Run doiq on the module's directory with its own build file, or keep a Dockerfile for it", j.Tool)
	case j.Packaging == "war" || j.Packaging == "ear":
		return nil, unsupported("%s packaging needs an application server (Tomcat, WildFly); only runnable jars are supported", j.Packaging)
	case j.Packaging == "pom":
		return nil, unsupported("pom packaging produces no runnable artifact")
	}

	c, err := carry(ctx, "/app")
	if err != nil {
		return nil, err
	}
	var notes []string
	had := map[string]bool{}

	// --- version ------------------------------------------------------------
	version := 0
	if img, ok := existingJavaImage(c); ok {
		version = javaImageVersion(img)
	}
	if version == 0 {
		version = j.Version
	}
	if version == 0 {
		version = DefaultJavaVersion
		notes = append(notes, fmt.Sprintf("No Java version found in the build file or Dockerfile; using Java %d (LTS).", version))
	}
	v := strconv.Itoa(version)

	// --- how it runs ------------------------------------------------------------
	layout, err := javaLayout(j)
	if err != nil && !c.hasStartCommand() {
		return nil, err
	}
	jarDest := path.Join(c.workdir, "app.jar")
	start := c.cmd
	entry := c.entrypoint
	if c.hasStartCommand() {
		lead := c.entrypoint
		if lead == nil {
			lead = c.cmd
		}
		text := instructionText(*lead)
		switch {
		case buildToolRunRe.MatchString(text):
			// `mvn spring-boot:run` in the runtime image is Java's `go run`:
			// the build tool will not exist in the new image.
			start, entry = nil, nil
			notes = append(notes, "`"+strings.Fields(text)[0]+"` replaced by running the built jar: the build tool is not in the runtime image.")
		default:
			if jar := jarToken(text); jar != "" {
				jarDest = resolveDir(c.workdir, jar)
			}
		}
	}

	// --- images -------------------------------------------------------------------
	builder, buildCmd := javaBuilder(j, v, c)
	runtimeImage, osFamily := "eclipse-temurin:"+v+"-jre", "debian"
	if c.existing && c.runtime.Family() == dockerfile.FamilyJava && isJavaRuntimeImage(c.runtime) {
		runtimeImage = c.runtime.Ref // already a JRE: keep the user's choice
		osFamily = c.runtime.OSFamily()
		had[RuleSlimBase] = true
	}
	runtimeImage, _, osFamily = keepCustomBase(c, dockerfile.FamilyJava, runtimeImage, "", osFamily, &notes)

	ports := c.ports
	if len(ports) == 0 {
		ports = []string{defaultJavaPort}
		notes = append(notes, "No EXPOSE found; documenting port "+defaultJavaPort+" (the Spring Boot default). EXPOSE does not publish anything, so a wrong guess is harmless.")
	}
	nonRoot := false
	switch {
	case c.user != "" && !userIsRoot(c.user):
		had[RuleNonRootUser] = true
	case lowPort(ports):
		notes = append(notes, "The app exposes a port below 1024, which an unprivileged user cannot bind; the container keeps running as root.")
	case c.user != "":
		notes = append(notes, "The existing Dockerfile explicitly runs as root; that is kept.")
	default:
		nonRoot = true
	}

	// --- build stage ------------------------------------------------------------------
	f := &dockerfile.File{Header: header(constants.StackJava, c.existing)}
	build := f.AddStage(builder, "build", "--- build stage: JDK, build tool and dependency cache never reach the runtime ---")
	build.Add("WORKDIR", "/src")
	if m := ctx.Packages.Maven; m != "" && j.Tool == detector.JavaToolMaven {
		build.Note("Internal Maven mirror, from .doiq.yml.", "RUN", "mkdir -p /root/.m2 && printf '%s' '<settings><mirrors><mirror><id>internal</id><mirrorOf>*</mirrorOf><url>"+m+"</url></mirror></mirrors></settings>' > /root/.m2/settings.xml")
	}
	setupCopies, warm := javaWarmup(j)
	build.Note("Build files first: the dependency download below is reused until they change.", "COPY", setupCopies)
	build.Note("Best effort: a plugin go-offline cannot resolve is fetched by the real build below.", "RUN", warm)
	build.Note("", "COPY", ". .")
	build.Add("RUN", buildCmd)
	build.Add("RUN", layout.collect)

	// --- runtime stage -------------------------------------------------------------------
	rt := f.AddStage(runtimeImage, "", "--- runtime stage: JRE only ---")
	if len(c.env) > 0 {
		rt.Add("ENV", envLine(c.env))
	}
	if pkgs := c.runtimePackages(); len(pkgs) > 0 {
		rt.Note("Runtime OS packages from the existing Dockerfile (build-only tools removed).", "RUN", osInstall(osFamily, pkgs))
	}
	if nonRoot {
		if osFamily == "alpine" {
			rt.Note("Unprivileged user for the app.", "RUN", "addgroup -S app && adduser -S -G app app")
		} else {
			rt.Note("Unprivileged user for the app.", "RUN", "groupadd --system app && useradd --system --gid app --no-create-home app")
		}
	}
	rt.Add("WORKDIR", c.workdir)
	chown := ""
	if nonRoot {
		chown = "--chown=app:app "
	}
	switch layout.kind {
	case "dist":
		rt.Note("The application's distribution: start script plus its libraries.", "COPY", "--from=build "+chown+"/out/app "+c.workdir)
	default:
		rt.Note("", "COPY", "--from=build "+chown+"/out/app.jar "+jarDest)
		if layout.kind == "classpath" {
			rt.Add("COPY", "--from=build "+chown+"/out/lib "+path.Join(c.workdir, "lib"))
		}
	}
	for _, in := range c.passthrough {
		rt.Raw("Carried over from the existing Dockerfile.", in)
	}
	for _, cc := range c.custom {
		rt.Raw("Carried over unchanged: DeployIQ does not regenerate this step.", cc.Instruction)
	}
	if c.health != nil {
		rt.Raw("Health check from the existing Dockerfile.", *c.health)
	}
	switch {
	case nonRoot:
		rt.Add("USER", "app")
	case c.user != "":
		rt.Add("USER", c.user)
	}
	rt.Add("EXPOSE", strings.Join(ports, " "))
	switch {
	case entry != nil || start != nil:
		if entry != nil {
			rt.Raw("", *entry)
		}
		if start != nil {
			rt.Raw("", *start)
		}
	default:
		argv := layout.run(jarDest, c.workdir)
		rt.Exec("ENTRYPOINT", argv)
		notes = append(notes, "Start command: "+strings.Join(argv, " ")+".")
	}

	// --- rules and estimate ----------------------------------------------------------------
	applied := []string{RuleMultiStage, RuleSlimBase, RuleDropBuildTools, RuleLayerCaching}
	if nonRoot || had[RuleNonRootUser] {
		applied = append(applied, RuleNonRootUser)
	}
	if c.existing {
		had[RuleMultiStage] = ctx.Existing.MultiStage()
		had[RuleDropBuildTools] = ctx.Existing.MultiStage() && isJavaRuntimeImage(c.runtime)
		had[RuleLayerCaching] = hadLayerCaching(ctx.Existing, "pom.xml", "build.gradle", "build.gradle.kts")
		if c.runtime.Custom() != nil {
			had[RuleSlimBase] = true
		}
	}
	improved, present := split(applied, had)

	artifact := analyzer.JavaArtifactMB(j)
	srcMB := analyzer.SourceMB(ctx.Root)
	after := analyzer.Profile{BaseImage: runtimeImage, SystemPackages: c.runtimePackages(), ArtifactMB: artifact}
	before, label := javaBefore(c, j, v, artifact, srcMB)

	if !analyzer.HasDockerignore(ctx.Root) {
		notes = append(notes, "No .dockerignore: `COPY . .` will send target/, build/ and .git into the build. Add a .dockerignore.")
	}
	return &Result{
		Dockerfile:     dockerfile.Generate(f),
		RulesApplied:   improved,
		AlreadyPresent: present,
		Notes:          notes,
		Before:         before,
		After:          after,
		BeforeLabel:    label,
	}, nil
}

// javaRunLayout is what the build stage leaves in /out and how to start it.
type javaRunLayout struct {
	kind    string // "jar" (self-contained), "classpath" (jar + lib/), "dist" (Gradle installDist)
	collect string // shell that fills /out
	main    string
}

func (l javaRunLayout) run(jar, workdir string) []string {
	switch l.kind {
	case "dist":
		return []string{path.Join(workdir, "bin", "start")}
	case "classpath":
		return []string{"java", "-cp", jar + ":" + path.Join(workdir, "lib") + "/*", l.main}
	}
	return []string{"java", "-jar", jar}
}

// javaLayout decides how the artifact is packaged. An error means the project
// produces nothing runnable doiq can identify.
func javaLayout(j *detector.JavaInfo) (javaRunLayout, error) {
	out := "target"
	if j.Tool == detector.JavaToolGradle {
		out = "build/libs"
	}
	pick := fmt.Sprintf(jarPick, out)
	switch {
	case j.SpringBoot:
		return javaRunLayout{kind: "jar", collect: `mkdir -p /out && cp "$(` + pick + `)" /out/app.jar`}, nil
	case j.Tool == detector.JavaToolGradle && j.Application:
		return javaRunLayout{kind: "dist", collect: `mkdir -p /out/app && cp -r build/install/*/. /out/app/ && cd /out/app/bin && rm -f *.bat && mv "$(ls | head -n 1)" start`}, nil
	case j.Tool == detector.JavaToolMaven && j.MainClass != "":
		return javaRunLayout{kind: "classpath", main: j.MainClass,
			collect: `mkdir -p /out target/lib && cp "$(` + pick + `)" /out/app.jar && cp -r target/lib /out/lib`}, nil
	}
	if j.Tool == detector.JavaToolGradle {
		return javaRunLayout{}, unsupported("could not tell how to run this Gradle project: it uses neither Spring Boot nor the `application` plugin. Add `application { mainClass = ... }`, or a Dockerfile with a CMD")
	}
	return javaRunLayout{}, unsupported("could not tell how to run this Maven project: it is not Spring Boot and declares no main class (maven-jar-plugin manifest or exec-maven-plugin). Add one, or a Dockerfile with a CMD")
}

// javaBuilder picks the build image and the build command.
func javaBuilder(j *detector.JavaInfo, v string, c *carried) (string, string) {
	if img, ok := existingJavaBuilder(c); ok {
		// A pinned build image is the team's decision; reuse it.
		return img.Ref, javaBuildCmd(j, img.Name())
	}
	switch {
	case j.Wrapper:
		return "eclipse-temurin:" + v + "-jdk", javaBuildCmd(j, "wrapper")
	case j.Tool == detector.JavaToolGradle:
		return "gradle:jdk" + v, javaBuildCmd(j, "gradle")
	}
	return "maven:3.9-eclipse-temurin-" + v, javaBuildCmd(j, "maven")
}

func javaBuildCmd(j *detector.JavaInfo, image string) string {
	if j.Tool == detector.JavaToolGradle {
		bin := "gradle"
		if j.Wrapper && image != "gradle" {
			bin = "./gradlew"
		}
		task := "bootJar"
		if !j.SpringBoot {
			task = "installDist"
		}
		return bin + " --no-daemon -q " + task + " -x test"
	}
	bin := "mvn"
	if j.Wrapper && image != "maven" {
		bin = "./mvnw"
	}
	cmd := bin + " -B -q -DskipTests package"
	if !j.SpringBoot {
		cmd += " dependency:copy-dependencies -DincludeScope=runtime -DoutputDirectory=target/lib"
	}
	return cmd
}

// javaWarmup returns the build-file COPY and the dependency pre-download.
func javaWarmup(j *detector.JavaInfo) (string, string) {
	if j.Tool == detector.JavaToolGradle {
		files := "build.gradle* settings.gradle* gradle.properties* ./"
		bin := "gradle"
		if j.Wrapper {
			files = "gradlew build.gradle* settings.gradle* gradle.properties* ./\nCOPY gradle ./gradle"
			bin = "chmod +x gradlew && ./gradlew"
		}
		return files, bin + " --no-daemon -q dependencies > /dev/null 2>&1 || true"
	}
	files := "pom.xml ./"
	bin := "mvn"
	if j.Wrapper {
		files = "mvnw pom.xml ./\nCOPY .mvn ./.mvn"
		bin = "chmod +x mvnw && ./mvnw"
	}
	return files, bin + " -B -q -DskipTests dependency:go-offline > /dev/null 2>&1 || true"
}

var (
	buildToolRunRe = regexp.MustCompile(`^(\./)?(mvn|mvnw|gradle|gradlew)\b`)
	jarTokenRe     = regexp.MustCompile(`[\w./${}-]+\.jar\b`)
	javaVersionRe  = regexp.MustCompile(`(?:temurin|jdk|jre|openjdk|corretto|graalvm|zulu|semeru|liberica|sapmachine)-?(\d+)`)
	leadingNumRe   = regexp.MustCompile(`^(\d+)`)
)

// jarToken finds the jar a start command runs, unless it is a glob or uses
// variables (then the default location is used).
func jarToken(cmd string) string {
	m := jarTokenRe.FindString(cmd)
	if m == "" || strings.ContainsAny(m, "*$") {
		return ""
	}
	return m
}

// javaImageVersion reads the Java major from an image tag:
// maven:3.9.6-eclipse-temurin-17, gradle:8.5-jdk21, eclipse-temurin:21-jre.
func javaImageVersion(img dockerfile.Image) int {
	tag := strings.ToLower(img.Tag)
	if c := img.Custom(); c != nil && c.Version != "" {
		tag = c.Version
	}
	if m := javaVersionRe.FindStringSubmatch(tag); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	switch img.Name() {
	case "eclipse-temurin", "openjdk", "amazoncorretto", "ibm-semeru-runtimes", "sapmachine":
		if m := leadingNumRe.FindStringSubmatch(tag); m != nil {
			n, _ := strconv.Atoi(m[1])
			if n >= 8 && n < 40 {
				return n
			}
		}
	}
	if img.Custom() != nil {
		if m := leadingNumRe.FindStringSubmatch(tag); m != nil {
			n, _ := strconv.Atoi(m[1])
			return n
		}
	}
	return 0
}

func existingJavaImage(c *carried) (dockerfile.Image, bool) {
	if c.df == nil {
		return dockerfile.Image{}, false
	}
	for _, st := range c.df.Stages {
		if st.Image.Family() == dockerfile.FamilyJava {
			return st.Image, true
		}
	}
	return dockerfile.Image{}, false
}

func existingJavaBuilder(c *carried) (dockerfile.Image, bool) {
	if c.df == nil {
		return dockerfile.Image{}, false
	}
	for _, st := range c.df.Stages {
		if n := st.Image.Name(); n == "maven" || n == "gradle" {
			return st.Image, true
		}
	}
	return dockerfile.Image{}, false
}

// isJavaRuntimeImage reports a JRE (or distroless java) rather than a build
// image: no Maven, no Gradle, no full JDK.
func isJavaRuntimeImage(img dockerfile.Image) bool {
	n := img.Name()
	if n == "maven" || n == "gradle" {
		return false
	}
	t := strings.ToLower(img.Tag)
	return strings.Contains(t, "jre") || strings.Contains(img.Repository, "jre") || strings.Contains(img.Repository, "distroless/java")
}

func javaBefore(c *carried, j *detector.JavaInfo, v string, artifact, srcMB float64) (analyzer.Profile, string) {
	cache := analyzer.JavaDepsCacheMB(j)
	if !c.existing {
		img := "maven:3.9-eclipse-temurin-" + v
		if j.Tool == detector.JavaToolGradle {
			img = "gradle:jdk" + v
		}
		return analyzer.Profile{BaseImage: img, SourceMB: srcMB, ToolchainMB: cache, ArtifactMB: artifact * 2},
			"conventional single-stage baseline (no Dockerfile found)"
	}
	if c.multiStage && isJavaRuntimeImage(c.runtime) {
		return analyzer.Profile{BaseImage: c.runtime.Ref, SystemPackages: c.runtimePackages(), ArtifactMB: artifact}, "current Dockerfile"
	}
	// Single stage: the build image, the dependency cache, sources and the
	// build output (classes plus the jar) all ship.
	return analyzer.Profile{BaseImage: c.runtime.Ref, SystemPackages: c.packages, SourceMB: srcMB, ToolchainMB: cache, ArtifactMB: artifact * 2}, "current Dockerfile"
}
