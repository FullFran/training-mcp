package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// sandbox is a throwaway HOME and repository root. Fake tools live in
// HOME/.nix-profile/bin, where the scripts must find them under cron's PATH.
type sandbox struct {
	t             *testing.T
	home, root    string
	tools, backup string
}

func newSandbox(t *testing.T) *sandbox {
	t.Helper()
	dir := t.TempDir()
	s := &sandbox{
		t:      t,
		home:   filepath.Join(dir, "home"),
		root:   filepath.Join(dir, "repo"),
		tools:  filepath.Join(dir, "home", ".nix-profile", "bin"),
		backup: filepath.Join(dir, "home", "backups", "training-mcp"),
	}
	for _, d := range []string{s.tools, filepath.Join(s.root, "scripts"), filepath.Join(s.root, ".credentials")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"backup.sh", "install-backup-cron.sh", "restore-check.sh"} {
		body, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(s.root, "scripts", name), body, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	s.write(filepath.Join(s.root, ".credentials", "dokploy.env"), "WEB_URL=https://example.invalid/app\n", 0o600)
	s.tool("crontab", `if [ "$1" = -l ]; then cat "$HOME/crontab"; else cat >"$HOME/crontab"; fi`)
	s.tool("notify-send", `echo "$@" >>"$HOME/notified"`)
	s.tool("sqlite3", `case "$2" in *integrity_check*) echo ok ;; *) echo 7 ;; esac`)
	s.tool("curl", `echo called >>"$HOME/curl-calls"; while [ $# -gt 1 ]; do [ "$1" = -o ] && out="$2"; shift; done; printf 'SQLite format 3 fresh' >"$out"`)
	return s
}

func (s *sandbox) write(path, body string, mode os.FileMode) {
	s.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		s.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		s.t.Fatal(err)
	}
}

func (s *sandbox) tool(name, body string) {
	s.write(filepath.Join(s.tools, name), "#!/bin/sh\n"+body+"\n", 0o755)
}

// run executes a script with the same minimal environment cron provides.
func (s *sandbox) run(script string, args ...string) (string, error) {
	s.t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		s.t.Skip("bash not available")
	}
	cmd := exec.Command(bash, append([]string{filepath.Join(s.root, "scripts", script)}, args...)...)
	path := "/usr/bin:/bin"
	if script != "backup.sh" {
		// The real crontab belongs to the user running the tests and ignores HOME,
		// so the installer must always find the fake first.
		path = s.tools + ":" + path
	}
	tmp := filepath.Join(s.home, "tmp")
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		s.t.Fatal(err)
	}
	cmd.Env = []string{"HOME=" + s.home, "PATH=" + path, "TMPDIR=" + tmp}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (s *sandbox) today() string {
	return filepath.Join(s.backup, "training-"+time.Now().Format("2006-01-02")+".db")
}

func (s *sandbox) read(path string) string {
	s.t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		s.t.Fatal(err)
	}
	return string(body)
}

func TestBackupFindsItsToolsUnderCronPath(t *testing.T) {
	s := newSandbox(t)
	out, err := s.run("backup.sh")
	if err != nil {
		t.Fatalf("backup failed: %v\n%s", err, out)
	}
	if got := s.read(s.today()); got != "SQLite format 3 fresh" {
		t.Fatalf("today's copy = %q", got)
	}
	if !strings.Contains(out, "7 sesiones, 7 series") {
		t.Fatalf("output = %q", out)
	}
	if _, err := os.Stat(filepath.Join(s.home, "notified")); err == nil {
		t.Fatal("a successful backup sent a failure notification")
	}
}

func TestFailedBackupKeepsThePreviousCopyAndNotifies(t *testing.T) {
	for name, curl := range map[string]string{
		"download fails": `exit 22`,
		"not a database": `while [ $# -gt 1 ]; do [ "$1" = -o ] && out="$2"; shift; done; printf 'garbage' >"$out"`,
	} {
		t.Run(name, func(t *testing.T) {
			s := newSandbox(t)
			s.write(s.today(), "good copy", 0o644)
			s.tool("curl", curl)
			if out, err := s.run("backup.sh"); err == nil {
				t.Fatalf("backup reported success:\n%s", out)
			}
			if got := s.read(s.today()); got != "good copy" {
				t.Fatalf("failure overwrote today's copy with %q", got)
			}
			entries, err := os.ReadDir(s.backup)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 {
				t.Fatalf("failure left stray files: %v", entries)
			}
			if !strings.Contains(s.read(filepath.Join(s.home, "notified")), "Training backup failed") {
				t.Fatal("failure sent no notification")
			}
		})
	}
}

func TestDailyModeSkipsOnceTodayIsBackedUp(t *testing.T) {
	s := newSandbox(t)
	s.write(s.today(), "good copy", 0o644)
	out, err := s.run("backup.sh", "--daily")
	if err != nil {
		t.Fatalf("daily run failed: %v\n%s", err, out)
	}
	if out != "" {
		t.Fatalf("a skipped run should be silent, got %q", out)
	}
	if _, err := os.Stat(filepath.Join(s.home, "curl-calls")); err == nil {
		t.Fatal("daily mode downloaded although today's copy exists")
	}
}

func TestManualBackupRefreshesTodaysCopy(t *testing.T) {
	s := newSandbox(t)
	s.write(s.today(), "older copy", 0o644)
	if out, err := s.run("backup.sh"); err != nil {
		t.Fatalf("backup failed: %v\n%s", err, out)
	}
	if got := s.read(s.today()); got != "SQLite format 3 fresh" {
		t.Fatalf("manual run kept %q", got)
	}
}

func TestBackupStillAcceptsADestinationArgument(t *testing.T) {
	s := newSandbox(t)
	dest := filepath.Join(s.home, "elsewhere")
	if out, err := s.run("backup.sh", dest); err != nil {
		t.Fatalf("backup failed: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(dest, filepath.Base(s.today()))); err != nil {
		t.Fatal(err)
	}
}

func TestBackupCopiesArePrivate(t *testing.T) {
	s := newSandbox(t)
	if out, err := s.run("backup.sh"); err != nil {
		t.Fatalf("backup failed: %v\n%s", err, out)
	}
	info, err := os.Stat(s.today())
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("copy mode = %o, want 600", got)
	}
}

func TestInstallerReplacesItsOwnEntriesAndNoOtherProjects(t *testing.T) {
	s := newSandbox(t)
	foreign := "0 2 * * * /home/x/other-project/scripts/backup.sh"
	s.write(filepath.Join(s.home, "crontab"),
		"0 1 * * * /usr/bin/unrelated\n"+foreign+"\n17 7 * * * /old/clone/training-mcp/scripts/backup.sh >>/tmp/log 2>&1\n", 0o644)
	if out, err := s.run("install-backup-cron.sh"); err != nil {
		t.Fatalf("install failed: %v\n%s", err, out)
	}
	if out, err := s.run("install-backup-cron.sh"); err != nil {
		t.Fatalf("second install failed: %v\n%s", err, out)
	}
	got := s.read(filepath.Join(s.home, "crontab"))
	if !strings.Contains(got, "/usr/bin/unrelated") || !strings.Contains(got, foreign) {
		t.Fatalf("installer dropped another project's entry:\n%s", got)
	}
	if strings.Contains(got, "/old/clone") {
		t.Fatalf("installer kept the old clone's entry:\n%s", got)
	}
	entry := "17 * * * * " + filepath.Join(s.root, "scripts", "backup.sh") + " --daily"
	if strings.Count(got, entry) != 1 {
		t.Fatalf("crontab = \n%s", got)
	}
}
