package scripts

import (
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

func TestWatchdogStaysQuietWhileCopiesAreRecent(t *testing.T) {
	for name, daysAgo := range map[string]int{"today": 0, "yesterday": 1} {
		t.Run(name, func(t *testing.T) {
			s := newSandbox(t)
			s.configureWatchdog(copyName(9), copyName(daysAgo))
			out, err := s.run("check-offsite.sh")
			if err != nil {
				t.Fatalf("a recent copy failed the check: %v\n%s", err, out)
			}
			if !strings.Contains(out, copyName(daysAgo)) {
				t.Fatalf("output does not name the newest copy: %s", out)
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
}

func TestWatchdogAlertsWhenTheBucketCannotBeRead(t *testing.T) {
	s := newSandbox(t)
	s.configureWatchdog(copyName(0))
	s.write(filepath.Join(s.home, "list-fails"), "", 0o644)
	out, err := s.run("check-offsite.sh")
	if err == nil {
		t.Fatalf("an unreadable bucket passed the check:\n%s", out)
	}
	if !strings.Contains(out, "could not read the bucket") {
		t.Fatalf("output = %q", out)
	}
}

func TestWatchdogAlertsWhenItsConfigurationIsMissing(t *testing.T) {
	s := newSandbox(t)
	out, err := s.run("check-offsite.sh")
	if err == nil {
		t.Fatalf("a missing configuration passed the check:\n%s", out)
	}
	if !strings.Contains(out, "r2-watch.env") {
		t.Fatalf("output = %q", out)
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
