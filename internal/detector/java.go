package detector

import (
	"encoding/xml"
	"fmt"
	"regexp"
	"strings"

	"github.com/dhyaneshsiddhartha15/DeployIQ/pkg/constants"
)

// Java build tools.
const (
	JavaToolMaven  = "maven"
	JavaToolGradle = "gradle"
)

// JavaInfo describes a Java repository.
type JavaInfo struct {
	// Tool is maven or gradle.
	Tool string

	// Wrapper is true when ./mvnw or ./gradlew is committed with its
	// wrapper directory: the project pins its own build-tool version, and
	// the build should use it.
	Wrapper bool

	// Version is the Java major version the project compiles for, or 0.
	Version int

	// SpringBoot is true for a Spring Boot application: its build produces
	// one self-contained executable jar.
	SpringBoot bool

	// Application is true for a Gradle project using the `application`
	// plugin: `installDist` produces a runnable layout with a start script.
	Application bool

	// MainClass is the entry point when one is declared (needed for a plain
	// jar run with a classpath).
	MainClass string

	// Name is the artifactId / rootProject.name, used for installDist paths.
	Name string

	// Packaging is the Maven packaging (jar, war, pom).
	Packaging string

	// MultiModule is true for a Maven reactor or a Gradle multi-project
	// build: which module is the service cannot be inferred (Phase 1.4).
	MultiModule bool

	// KotlinDSL is true for build.gradle.kts.
	KotlinDSL bool
}

type pomFile struct {
	ArtifactID string `xml:"artifactId"`
	Packaging  string `xml:"packaging"`
	Parent     struct {
		GroupID    string `xml:"groupId"`
		ArtifactID string `xml:"artifactId"`
	} `xml:"parent"`
	Properties struct {
		Inner []struct {
			XMLName xml.Name
			Value   string `xml:",chardata"`
		} `xml:",any"`
	} `xml:"properties"`
	Modules struct {
		Module []string `xml:"module"`
	} `xml:"modules"`
	Build struct {
		Plugins struct {
			Plugin []struct {
				ArtifactID    string `xml:"artifactId"`
				Configuration struct {
					Release   string `xml:"release"`
					Source    string `xml:"source"`
					MainClass string `xml:"mainClass"`
					Archive   struct {
						Manifest struct {
							MainClass string `xml:"mainClass"`
						} `xml:"manifest"`
					} `xml:"archive"`
				} `xml:"configuration"`
			} `xml:"plugin"`
		} `xml:"plugins"`
	} `xml:"build"`
}

func detectJava(root string) (*Result, error) {
	if raw, err := readManifest(root, constants.ManifestPomXML); err != nil {
		return nil, err
	} else if raw != nil {
		info, err := parsePom(raw)
		if err != nil {
			return nil, err
		}
		info.Wrapper = exists(root, "mvnw") && isDir(root, ".mvn")
		return &Result{Stack: constants.StackJava, Manifest: constants.ManifestPomXML, Java: info}, nil
	}

	for _, name := range []string{constants.ManifestBuildGradleKts, constants.ManifestBuildGradle} {
		raw, err := readManifest(root, name)
		if err != nil {
			return nil, err
		}
		if raw == nil {
			continue
		}
		info := parseGradle(string(raw))
		info.KotlinDSL = strings.HasSuffix(name, ".kts")
		info.Wrapper = exists(root, "gradlew") && isDir(root, "gradle/wrapper")
		for _, s := range []string{"settings.gradle.kts", "settings.gradle"} {
			if settings, err := readManifest(root, s); err == nil && settings != nil {
				parseGradleSettings(string(settings), info)
			}
		}
		return &Result{Stack: constants.StackJava, Manifest: name, Java: info}, nil
	}
	return nil, nil
}

func parsePom(raw []byte) (*JavaInfo, error) {
	var pom pomFile
	if err := xml.Unmarshal(raw, &pom); err != nil {
		return nil, fmt.Errorf("detector: pom.xml is not valid XML: %w", err)
	}
	info := &JavaInfo{Tool: JavaToolMaven, Name: pom.ArtifactID, Packaging: pom.Packaging}
	if info.Packaging == "" {
		info.Packaging = "jar"
	}
	info.MultiModule = len(pom.Modules.Module) > 0
	info.SpringBoot = pom.Parent.ArtifactID == "spring-boot-starter-parent"

	props := map[string]string{}
	for _, p := range pom.Properties.Inner {
		props[p.XMLName.Local] = strings.TrimSpace(p.Value)
	}
	for _, key := range []string{"maven.compiler.release", "java.version", "maven.compiler.source", "maven.compiler.target"} {
		if v := javaMajor(props[key]); v > 0 {
			info.Version = v
			break
		}
	}
	for _, pl := range pom.Build.Plugins.Plugin {
		switch pl.ArtifactID {
		case "spring-boot-maven-plugin":
			info.SpringBoot = true
			if pl.Configuration.MainClass != "" {
				info.MainClass = pl.Configuration.MainClass
			}
		case "maven-compiler-plugin":
			for _, v := range []string{pl.Configuration.Release, pl.Configuration.Source} {
				if n := javaMajor(resolveProp(v, props)); n > 0 && info.Version == 0 {
					info.Version = n
				}
			}
		case "maven-jar-plugin", "maven-assembly-plugin", "maven-shade-plugin":
			if mc := pl.Configuration.Archive.Manifest.MainClass; mc != "" {
				info.MainClass = mc
			}
		case "exec-maven-plugin":
			if mc := pl.Configuration.MainClass; mc != "" && info.MainClass == "" {
				info.MainClass = mc
			}
		}
	}
	info.MainClass = resolveProp(info.MainClass, props)
	return info, nil
}

var propRe = regexp.MustCompile(`^\$\{([^}]+)\}$`)

func resolveProp(v string, props map[string]string) string {
	if m := propRe.FindStringSubmatch(strings.TrimSpace(v)); m != nil {
		return props[m[1]]
	}
	return strings.TrimSpace(v)
}

var javaMajorRe = regexp.MustCompile(`^(?:1\.)?(\d+)`)

// javaMajor maps "17", "1.8", "21.0.2" to 17, 8, 21.
func javaMajor(v string) int {
	m := javaMajorRe.FindStringSubmatch(strings.TrimSpace(v))
	if m == nil {
		return 0
	}
	n := atoiSafe(m[1])
	if n < 6 || n > 40 {
		return 0
	}
	return n
}

var (
	gradleToolchainRe = regexp.MustCompile(`JavaLanguageVersion\.of\(\s*(\d+)\s*\)`)
	gradleCompatRe    = regexp.MustCompile(`(?:source|target)Compatibility\s*=\s*(?:JavaVersion\.VERSION_(?:1_)?(\d+)|['"]?(?:1\.)?(\d+))`)
	gradleMainRe      = regexp.MustCompile(`(?:mainClass(?:Name)?\s*(?:\.set\()?\s*=?\s*|mainClass\.set\()\s*['"]([\w.$]+)['"]`)
	gradleNameRe      = regexp.MustCompile(`rootProject\.name\s*=\s*['"]([^'"]+)['"]`)
	gradleIncludeRe   = regexp.MustCompile(`(?m)^\s*include\s*[\(\s]`)
)

func parseGradle(src string) *JavaInfo {
	info := &JavaInfo{Tool: JavaToolGradle, Packaging: "jar"}
	info.SpringBoot = strings.Contains(src, "org.springframework.boot")
	info.Application = regexp.MustCompile(`(?m)(^|\s|\()['"]?application['"]?(\)|\s|$)|id\s*\(?\s*['"]application['"]`).MatchString(src)
	if m := gradleToolchainRe.FindStringSubmatch(src); m != nil {
		info.Version = atoiSafe(m[1])
	} else if m := gradleCompatRe.FindStringSubmatch(src); m != nil {
		info.Version = atoiSafe(m[1] + m[2])
	}
	if m := gradleMainRe.FindStringSubmatch(src); m != nil {
		info.MainClass = m[1]
	}
	if strings.Contains(src, "war") && regexp.MustCompile(`id\s*\(?\s*['"]war['"]|apply\s+plugin:\s*['"]war['"]`).MatchString(src) {
		info.Packaging = "war"
	}
	return info
}

func parseGradleSettings(src string, info *JavaInfo) {
	if m := gradleNameRe.FindStringSubmatch(src); m != nil {
		info.Name = m[1]
	}
	if gradleIncludeRe.MatchString(src) {
		info.MultiModule = true
	}
}

func atoiSafe(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return n
		}
		n = n*10 + int(r-'0')
	}
	return n
}
