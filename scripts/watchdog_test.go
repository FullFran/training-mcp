package scripts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// configureWatchdog stands in for the bucket: curl prints HOME/listing, or fails
// when HOME/list-fails exists.
func (s *sandbox) configureWatchdog(keys ...string) {
	s.t.Helper()
	s.write(filepath.Join(s.root, ".credentials", "r2-watch.env"), strings.Join([]string{
		"R2_ENDPOINT=https://account.r2.example.invalid",
		"R2_BUCKET=training-backups",
		"R2_ACCESS_KEY_ID=access-id",
		"R2_SECRET_ACCESS_KEY=" + fakeSecret,
	}, "\n")+"\n", 0o600)
	listing := ""
	for _, key := range keys {
		listing += "<Key>" + key + "</Key>"
	}
	s.write(filepath.Join(s.home, "listing"), listing, 0o600)
	s.tool("curl", `
echo "$*" >>"$HOME/curl-args"
cat >>"$HOME/curl-config"
[ ! -e "$HOME/list-fails" ] || exit 22
cat "$HOME/listing"`)
}

func copyName(daysAgo int) string {
	return "training-" + time.Now().AddDate(0, 0, -daysAgo).Format("2006-01-02") + ".db.gpg"
}

func (s *sandbox) wasNotified() string {
	s.t.Helper()
	body, err := os.ReadFile(filepath.Join(s.home, "notified"))
	if err != nil {
		return ""
	}
	return string(body)
}

func TestWatchdogStaysQuietWhileCopiesAreRecent(t *testing.T) {
	for name, daysAgo := range map[string]int{"today": 0, "yesterday": 1} {
		t.Run(name, func(t *testing.T) {
			s := newSandbox(t)
			s.configureWatchdog(copyName(9), copyName(daysAgo))
			if out, err := s.run("check-offsite.sh"); err != nil {
				t.Fatalf("a recent copy failed the check: %v\n%s", err, out)
			}
			if got := s.wasNotified(); got != "" {
				t.Fatalf("notified about a recent copy: %s", got)
			}
		})
	}
}

func TestWatchdogAlertsWhenTheNewestCopyIsStale(t *testing.T) {
	s := newSandbox(t)
	s.configureWatchdog(copyName(9), copyName(2))
	out, err := s.run("check-offsite.sh")
	if err == nil {
		t.Fatalf("a stale copy passed the check:\n%s", out)
	}
	date := strings.TrimSuffix(strings.TrimPrefix(copyName(2), "training-"), ".db.gpg")
	if !strings.Contains(out, date) {
		t.Fatalf("output does not name the newest copy: %s", out)
	}
	if !strings.Contains(s.wasNotified(), "Training off-site backup") {
		t.Fatalf("notification = %q", s.wasNotified())
	}
}

func TestWatchdogAlertsWhenTheBucketIsEmpty(t *testing.T) {
	s := newSandbox(t)
	s.configureWatchdog()
	out, err := s.run("check-offsite.sh")
	if err == nil {
		t.Fatalf("an empty bucket passed the check:\n%s", out)
	}
	if !strings.Contains(out, "no copies") {
		t.Fatalf("output = %q", out)
	}
	if s.wasNotified() == "" {
		t.Fatal("an empty bucket sent no notification")
	}
}

func TestWatchdogAlertsWhenTheBucketCannotBeRead(t *testing.T) {
	s := newSandbox(t)
	s.configureWatchdog(copyName(0))
	s.write(filepath.Join(s.home, "list-fails"), "", 0o644)
	if out, err := s.run("check-offsite.sh"); err == nil {
		t.Fatalf("an unreadable bucket passed the check:\n%s", out)
	}
	if s.wasNotified() == "" {
		t.Fatal("an unreadable bucket sent no notification")
	}
}

func TestWatchdogAlertsWhenItsConfigurationIsMissing(t *testing.T) {
	s := newSandbox(t)
	if out, err := s.run("check-offsite.sh"); err == nil {
		t.Fatalf("a missing configuration passed the check:\n%s", out)
	}
	if s.wasNotified() == "" {
		t.Fatal("a missing configuration sent no notification")
	}
}

func TestWatchdogKeepsTheSecretOutOfCurlArguments(t *testing.T) {
	s := newSandbox(t)
	s.configureWatchdog(copyName(0))
	if out, err := s.run("check-offsite.sh"); err != nil {
		t.Fatalf("check failed: %v\n%s", err, out)
	}
	args := s.read(filepath.Join(s.home, "curl-args"))
	if strings.Contains(args, fakeSecret) {
		t.Fatal("the R2 secret reached curl's arguments, visible to any process")
	}
	if !strings.Contains(args, "https://account.r2.example.invalid/training-backups/?list-type=2&prefix=training-") {
		t.Fatalf("listed elsewhere: %s", args)
	}
	if !strings.Contains(s.read(filepath.Join(s.home, "curl-config")), "access-id:"+fakeSecret) {
		t.Fatal("curl did not receive the credentials through its config")
	}
}

func TestCronInstallerAddsTheWatchdogOnceAndKeepsOtherEntries(t *testing.T) {
	s := newSandbox(t)
	other := "5 5 * * * /elsewhere/other-project/scripts/check-offsite.sh"
	s.write(filepath.Join(s.home, "crontab"), other+"\n", 0o600)
	for range 2 {
		if out, err := s.run("install-backup-cron.sh"); err != nil {
			t.Fatalf("install failed: %v\n%s", err, out)
		}
	}
	crontab := s.read(filepath.Join(s.home, "crontab"))
	if got := strings.Count(crontab, filepath.Join(s.root, "scripts", "check-offsite.sh")); got != 1 {
		t.Fatalf("watchdog entries = %d in:\n%s", got, crontab)
	}
	if got := strings.Count(crontab, filepath.Join(s.root, "scripts", "backup.sh")); got != 1 {
		t.Fatalf("backup entries = %d in:\n%s", got, crontab)
	}
	if !strings.Contains(crontab, other) {
		t.Fatalf("another project's entry was lost:\n%s", crontab)
	}
}
