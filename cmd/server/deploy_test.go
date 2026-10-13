package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A command-level deployment rehearsal: Docker is replaced with a local fake,
// so these tests cannot contact a daemon, stop a real app, or alter a database.
func TestDeploymentSwitchoverAndResume(t *testing.T) {
	for _, scenario := range []string{"success", "unhealthy", "proxy-failure", "proxy-starting", "cutover-failure"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			bin := filepath.Join(root, "mockbin")
			if err := os.MkdirAll(filepath.Join(root, "scripts"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(bin, 0755); err != nil {
				t.Fatal(err)
			}
			script, err := os.ReadFile("../../scripts/deploy.sh")
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(root, "scripts/deploy.sh"), script, 0755); err != nil {
				t.Fatal(err)
			}
			fake := `#!/usr/bin/env bash
set -eu
printf '%s\n' "$*" >> "$MOCK_STATE/log"
if [[ "$1" == inspect ]]; then
 case "$3" in
  *Health*) if [[ "${MOCK_FAILURE:-}" == unhealthy && "$4" == app-blue ]]; then echo unhealthy; else echo healthy; fi ;;
  *Config.Cmd*) echo active.Caddyfile ;;
  *Image*) echo sha256:checked-candidate ;;
 esac
 exit 0
fi
[[ "$1" == compose ]] || exit 2
shift 5
command=$1; shift
case "$command" in
 config) printf '%s\n' '{"configs":{"caddyfile":{"content":"example.test {\n reverse_proxy app:8080\n}\n"}}}' ;;
 ps) if [[ "${1:-}" == -q ]]; then service=${!#}; [[ ! -f "$MOCK_STATE/$service" ]] || echo "$service"; fi ;;
 up) for service in "$@"; do case "$service" in db|goatcounter|caddy|app-blue|app-green) touch "$MOCK_STATE/$service";; esac; done ;;
 stop) service=${!#}; rm -f "$MOCK_STATE/$service" ;;
 run) if [[ "${MOCK_FAILURE:-}" == cutover-failure && " $* " == *' -action cutover '* ]]; then exit 1; fi ;;
 exec) if [[ " $* " == *' cat /etc/caddy/Caddyfile '* ]]; then printf 'example.test {\n reverse_proxy app:8080\n}\n'; elif [[ " $* " == *' caddy wget '* ]]; then [[ "${MOCK_FAILURE:-}" != proxy-failure ]] || exit 1; if [[ "${MOCK_FAILURE:-}" == proxy-starting && ! -f "$MOCK_STATE/proxy-started" ]]; then touch "$MOCK_STATE/proxy-started"; exit 1; fi; echo OK; elif [[ " $* " == *' caddy sh '* ]]; then printf 'example.test {\n reverse_proxy app:8080\n}\n'; fi ;;
 pull|cp) ;;
 *) exit 0 ;;
esac
`
			if err = os.WriteFile(filepath.Join(bin, "docker"), []byte(fake), 0755); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(bin, "flock"), []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(bin, "sleep"), []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
				t.Fatal(err)
			}
			for _, service := range []string{"db", "app", "caddy"} {
				if err = os.WriteFile(filepath.Join(root, service), nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			run := func(failure string) (string, error) {
				cmd := exec.Command("bash", filepath.Join(root, "scripts/deploy.sh"), "--no-git")
				cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "MOCK_STATE="+root, "MOCK_FAILURE="+failure)
				out, err := cmd.CombinedOutput()
				return string(out), err
			}
			failure := ""
			if scenario != "success" {
				failure = scenario
			}
			output, err := run(failure)
			if (scenario == "success" || scenario == "proxy-starting") && err != nil {
				t.Fatalf("%s: %v", output, err)
			}
			if scenario != "success" && scenario != "proxy-starting" && err == nil {
				t.Fatal("injected failure ignored")
			}
			raw, _ := os.ReadFile(filepath.Join(root, "log"))
			log := string(raw)
			if scenario == "unhealthy" || scenario == "proxy-failure" {
				if _, err = os.Stat(filepath.Join(root, "app")); err != nil {
					t.Fatal("old app stopped on failed health")
				}
				if strings.Contains(log, "-action cutover") {
					t.Fatal("unhealthy candidate caused cutover")
				}
				if scenario == "proxy-failure" && !strings.Contains(log, "caddy reload --config /config/previous.Caddyfile") {
					t.Fatal("failed proxy did not restore previous route")
				}
				return
			}
			markers := []string{"-action prepare", "--force-recreate app-blue", "caddy reload", "stop -t 45 app", "-action cutover"}
			previous := -1
			for _, marker := range markers {
				index := strings.Index(log, marker)
				if index <= previous {
					t.Fatalf("wrong switchover order at %s:\n%s", marker, log)
				}
				previous = index
			}
			if scenario == "cutover-failure" {
				state, _ := os.ReadFile(filepath.Join(root, ".deploy/transition"))
				if !strings.Contains(string(state), "stopped") {
					t.Fatal("failed cutover not persisted")
				}
				if _, err = os.Stat(filepath.Join(root, ".deploy/active")); !os.IsNotExist(err) {
					t.Fatal("writers activated on failed cutover")
				}
				if output, err = run(""); err != nil {
					t.Fatalf("resume: %s %v", output, err)
				}
				after, _ := os.ReadFile(filepath.Join(root, "log"))
				resume := string(after[len(raw):])
				if strings.Contains(resume, "pull") || strings.Contains(resume, "force-recreate") {
					t.Fatal("resume replaced checked candidate")
				}
			}
			marker, _ := os.ReadFile(filepath.Join(root, ".deploy/active"))
			if string(marker) != "blue\n" {
				t.Fatalf("activation: %q", marker)
			}
			if _, err = os.Stat(filepath.Join(root, ".deploy/transition")); !os.IsNotExist(err) {
				t.Fatal("completed transition not cleared")
			}
		})
	}
}

// A pull can replace the script itself. The old process must hand off before
// interpreting any deployment commands against the updated configuration.
func TestDeploymentReexecutesAfterPull(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"scripts", "mockbin", ".git"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	script, err := os.ReadFile("../../scripts/deploy.sh")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "scripts/deploy.sh"), script, 0755); err != nil {
		t.Fatal(err)
	}
	fakeGit := `#!/bin/sh
[ "$*" = "pull --ff-only" ] || exit 2
cat > scripts/deploy.sh <<'SCRIPT'
#!/bin/sh
[ "$1" = "--no-git" ] || exit 3
printf 'updated-script-executed\n'
SCRIPT
`
	for name, contents := range map[string]string{"git": fakeGit, "flock": "#!/bin/sh\nexit 0\n"} {
		if err := os.WriteFile(filepath.Join(root, "mockbin", name), []byte(contents), 0755); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("bash", filepath.Join(root, "scripts/deploy.sh"))
	cmd.Env = append(os.Environ(), "PATH="+filepath.Join(root, "mockbin")+":"+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "updated-script-executed") {
		t.Fatalf("handoff: %s %v", out, err)
	}
}
