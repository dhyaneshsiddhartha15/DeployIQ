package measure

import (
	"context"
	"os"
	"os/exec"
	"testing"
)

func TestParseTrivy(t *testing.T) {
	raw := []byte(`{"Results":[
	  {"Vulnerabilities":[
	    {"VulnerabilityID":"CVE-1","PkgName":"openssl","Severity":"CRITICAL"},
	    {"VulnerabilityID":"CVE-1","PkgName":"openssl","Severity":"CRITICAL"},
	    {"VulnerabilityID":"CVE-2","PkgName":"zlib","Severity":"HIGH"},
	    {"VulnerabilityID":"CVE-3","PkgName":"x","Severity":"MEDIUM"},
	    {"VulnerabilityID":"CVE-4","PkgName":"y","Severity":"LOW"},
	    {"VulnerabilityID":"CVE-5","PkgName":"z","Severity":"weird"}]},
	  {"Vulnerabilities":null}]}`)
	c, err := ParseTrivy(raw)
	if err != nil {
		t.Fatal(err)
	}
	if c.Critical != 1 || c.High != 1 || c.Medium != 1 || c.Low != 1 || c.Unknown != 1 || c.Total() != 5 {
		t.Fatalf("%+v", c)
	}
	if _, err := ParseTrivy([]byte("not json")); err == nil {
		t.Fatal("want error")
	}
	if got := extractJSON("2026 INFO downloading db\n{\"Results\":[]}"); got != `{"Results":[]}` {
		t.Fatal(got)
	}
}

func TestTagsAreUnique(t *testing.T) {
	a, b := Tag()
	c, _ := Tag()
	if a == c || a == b {
		t.Fatal("tags collide")
	}
}

// TestBuildAndMeasure needs Docker; it runs when DOIQ_DOCKER_TESTS=1.
func TestBuildAndMeasure(t *testing.T) {
	if os.Getenv("DOIQ_DOCKER_TESTS") != "1" {
		t.Skip("set DOIQ_DOCKER_TESTS=1 to run")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("no docker")
	}
	ctx := context.Background()
	d, err := New(ctx, []string{"GREETING=hi"})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	_, after := Tag()
	size, err := d.BuildBytes(ctx, dir, []byte("FROM busybox:1.36\nARG GREETING\nRUN echo $GREETING > /g\n"), after)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Remove(after)
	if size <= 0 || size > 50 {
		t.Fatalf("size %v", size)
	}
	if _, err := d.BuildBytes(ctx, dir, []byte("FROM busybox:1.36\nRUN exit 3\n"), after+"x"); err == nil {
		t.Fatal("failing build reported success")
	}
}
