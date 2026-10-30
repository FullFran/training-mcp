package scripts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fakeSecret = "s3cr3t-value"

// configureOffsite adds R2 credentials and fake curl and gpg that stand in for
// the bucket: an upload lands in HOME/bucket, and gpg marks what it encrypted.
func (s *sandbox) configureOffsite() {
	s.t.Helper()
	s.write(filepath.Join(s.root, ".credentials", "r2.env"), strings.Join([]string{
		"R2_ENDPOINT=https://account.r2.example.invalid",
		"R2_BUCKET=training-backups",
		"R2_ACCESS_KEY_ID=access-id",
		"R2_SECRET_ACCESS_KEY=" + fakeSecret,
		"BACKUP_PASSPHRASE=passphrase-value",
	}, "\n")+"\n", 0o600)
	s.tool("gpg", `
out=""; in=""; mode=""
while [ $# -gt 0 ]; do
  case "$1" in
    --output) out="$2"; shift ;;
    --symmetric) mode=enc ;;
    --decrypt) mode=dec ;;
    --passphrase-fd) cat <&"$2" >>"$HOME/gpg-passphrase"; shift ;;
    --*) ;;
    *) in="$1" ;;
  esac
  shift
done
echo "$mode" >>"$HOME/gpg-calls"
if [ "$mode" = enc ]; then { printf 'ENCRYPTED:'; cat "$in"; } >"$out"; else sed 's/^ENCRYPTED://' "$in" >"$out"; fi`)
	s.tool("curl", `
echo "$*" >>"$HOME/curl-args"
out=""; up=""; url=""
while [ $# -gt 0 ]; do
  case "$1" in
    -o) out="$2"; shift ;;
    -T) up="$2"; shift ;;
    -K) cat >>"$HOME/curl-config"; shift ;;
    http*) url="$1" ;;
  esac
  shift
done
case "$url" in
  *export.db) printf 'SQLite format 3 fresh' >"$out" ;;
  *list-type=2*) printf '<Key>training-2020-01-01.db.gpg</Key><Key>training-2020-01-02.db.gpg</Key>' ;;
  *)
    if [ -n "$up" ]; then
      [ ! -e "$HOME/upload-fails" ] || exit 22
      mkdir -p "$HOME/bucket"; cp "$up" "$HOME/bucket/${url##*/}"
    else
      echo "$url" >"$HOME/fetched-url"; cp "$HOME/bucket/stored" "$out"
    fi ;;
esac`)
}

func (s *sandbox) uploaded() string {
	return filepath.Join(s.home, "bucket", filepath.Base(s.today())+".gpg")
}

func TestBackupUploadsAnEncryptedCopyWithoutExposingSecrets(t *testing.T) {
	s := newSandbox(t)
	s.configureOffsite()
	if out, err := s.run("backup.sh"); err != nil {
		t.Fatalf("backup failed: %v\n%s", err, out)
	}
	if got := s.read(s.uploaded()); got != "ENCRYPTED:SQLite format 3 fresh" {
		t.Fatalf("uploaded object = %q", got)
	}
	args := s.read(filepath.Join(s.home, "curl-args"))
	if !strings.Contains(args, "https://account.r2.example.invalid/training-backups/"+filepath.Base(s.uploaded())) {
		t.Fatalf("upload went elsewhere:\n%s", args)
	}
	if !strings.Contains(args, "--aws-sigv4 aws:amz:auto:s3") {
		t.Fatalf("upload is not signed for R2:\n%s", args)
	}
	if strings.Contains(args, fakeSecret) {
		t.Fatal("the R2 secret reached curl's arguments, visible to any process")
	}
	if !strings.Contains(s.read(filepath.Join(s.home, "curl-config")), "access-id:"+fakeSecret) {
		t.Fatal("curl did not receive the credentials through its config")
	}
	if !strings.Contains(s.read(filepath.Join(s.home, "gpg-passphrase")), "passphrase-value") {
		t.Fatal("gpg did not receive the passphrase")
	}
	entries, err := os.ReadDir(s.backup)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".incoming") || strings.HasSuffix(e.Name(), ".gpg") {
			t.Fatalf("left %s behind", e.Name())
		}
	}
}

func TestFailedUploadKeepsLocalCopyNotifiesAndRetriesWithoutDownloading(t *testing.T) {
	s := newSandbox(t)
	s.configureOffsite()
	s.write(filepath.Join(s.home, "upload-fails"), "", 0o644)
	if out, err := s.run("backup.sh", "--daily"); err == nil {
		t.Fatalf("a failed upload reported success:\n%s", out)
	}
	if got := s.read(s.today()); got != "SQLite format 3 fresh" {
		t.Fatalf("local copy = %q", got)
	}
	if !strings.Contains(s.read(filepath.Join(s.home, "notified")), "Training backup failed") {
		t.Fatal("a failed upload sent no notification")
	}

	if err := os.Remove(filepath.Join(s.home, "upload-fails")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(s.home, "curl-args")); err != nil {
		t.Fatal(err)
	}
	if out, err := s.run("backup.sh", "--daily"); err != nil {
		t.Fatalf("retry failed: %v\n%s", err, out)
	}
	if _, err := os.Stat(s.uploaded()); err != nil {
		t.Fatalf("retry did not upload: %v", err)
	}
	if strings.Contains(s.read(filepath.Join(s.home, "curl-args")), "export.db") {
		t.Fatal("retry downloaded again although today's copy exists")
	}
}

func TestDailyModeStopsOnceTheOffsiteCopyExists(t *testing.T) {
	s := newSandbox(t)
	s.configureOffsite()
	if out, err := s.run("backup.sh", "--daily"); err != nil {
		t.Fatalf("first run failed: %v\n%s", err, out)
	}
	if err := os.Remove(filepath.Join(s.home, "curl-args")); err != nil {
		t.Fatal(err)
	}
	out, err := s.run("backup.sh", "--daily")
	if err != nil || out != "" {
		t.Fatalf("second run should be a silent no-op: %v %q", err, out)
	}
	if _, err := os.Stat(filepath.Join(s.home, "curl-args")); err == nil {
		t.Fatal("second run reached the network")
	}
}

func TestBackupWarnsWhenNoOffsiteCopyIsConfigured(t *testing.T) {
	s := newSandbox(t)
	out, err := s.run("backup.sh")
	if err != nil {
		t.Fatalf("backup failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "no off-site copy") {
		t.Fatalf("output does not warn:\n%s", out)
	}
}

func TestIncompleteOffsiteConfigurationFailsLoudly(t *testing.T) {
	s := newSandbox(t)
	s.configureOffsite()
	s.write(filepath.Join(s.root, ".credentials", "r2.env"), "R2_BUCKET=training-backups\n", 0o600)
	if out, err := s.run("backup.sh"); err == nil {
		t.Fatalf("incomplete configuration reported success:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(s.home, "bucket")); err == nil {
		t.Fatal("uploaded with an incomplete configuration")
	}
	if got := s.read(s.today()); got != "SQLite format 3 fresh" {
		t.Fatalf("a bad configuration cost the local copy: %q", got)
	}
}

func TestOffsiteConfigurationIsReadNeverExecuted(t *testing.T) {
	s := newSandbox(t)
	s.configureOffsite()
	s.write(filepath.Join(s.root, ".credentials", "r2.env"), strings.Join([]string{
		"# a comment",
		"",
		"R2_ENDPOINT=https://account.r2.example.invalid",
		"R2_BUCKET=training-backups",
		"R2_ACCESS_KEY_ID=access-id",
		"R2_SECRET_ACCESS_KEY=" + fakeSecret,
		"BACKUP_PASSPHRASE=correct horse=battery $(touch \"$HOME/executed\")",
	}, "\n")+"\n", 0o600)
	out, err := s.run("backup.sh")
	if err != nil {
		t.Fatalf("backup failed: %v\n%s", err, out)
	}
	if strings.Contains(out, "horse") || strings.Contains(out, "not found") {
		t.Fatalf("part of the passphrase reached the output:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(s.home, "executed")); err == nil {
		t.Fatal("a value in r2.env was executed")
	}
	got := s.read(filepath.Join(s.home, "gpg-passphrase"))
	if !strings.Contains(got, `correct horse=battery $(touch "$HOME/executed")`) {
		t.Fatalf("passphrase = %q", got)
	}
}

func TestManualRunWithAFailedUploadIsRetriedByDailyMode(t *testing.T) {
	s := newSandbox(t)
	s.configureOffsite()
	if out, err := s.run("backup.sh", "--daily"); err != nil {
		t.Fatalf("first run failed: %v\n%s", err, out)
	}
	s.write(filepath.Join(s.home, "upload-fails"), "", 0o644)
	if out, err := s.run("backup.sh"); err == nil {
		t.Fatalf("a failed upload reported success:\n%s", out)
	}
	if err := os.Remove(filepath.Join(s.home, "upload-fails")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(s.uploaded()); err != nil {
		t.Fatal(err)
	}
	if out, err := s.run("backup.sh", "--daily"); err != nil {
		t.Fatalf("retry failed: %v\n%s", err, out)
	}
	if _, err := os.Stat(s.uploaded()); err != nil {
		t.Fatalf("the newer copy never went off-site: %v", err)
	}
}

func TestRestoreCheckFetchesTheNewestObjectAndVerifiesIt(t *testing.T) {
	s := newSandbox(t)
	s.configureOffsite()
	s.write(filepath.Join(s.home, "bucket", "stored"), "ENCRYPTED:SQLite format 3 fresh", 0o644)
	out, err := s.run("restore-check.sh", "--no-boot")
	if err != nil {
		t.Fatalf("restore check failed: %v\n%s", err, out)
	}
	if !strings.Contains(s.read(filepath.Join(s.home, "fetched-url")), "training-2020-01-02.db.gpg") {
		t.Fatal("did not fetch the newest object")
	}
	if !strings.Contains(out, "7 sesiones, 7 series") {
		t.Fatalf("output = %q", out)
	}
	s.assertTmpEmpty()
}

func TestRestoreCheckRejectsWhatItCannotRestore(t *testing.T) {
	for name, breakIt := range map[string]func(*sandbox){
		"not a database": func(s *sandbox) {
			s.write(filepath.Join(s.home, "bucket", "stored"), "ENCRYPTED:garbage", 0o644)
		},
		"wrong passphrase": func(s *sandbox) { s.tool("gpg", `exit 2`) },
		"fails integrity": func(s *sandbox) {
			s.tool("sqlite3", `echo "*** in database main ***"`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			s := newSandbox(t)
			s.configureOffsite()
			s.write(filepath.Join(s.home, "bucket", "stored"), "ENCRYPTED:SQLite format 3 fresh", 0o644)
			breakIt(s)
			out, err := s.run("restore-check.sh", "--no-boot")
			if err == nil {
				t.Fatalf("restore check passed:\n%s", out)
			}
			if strings.TrimSpace(out) == "" {
				t.Fatal("failed without saying why")
			}
			s.assertTmpEmpty()
		})
	}
}

func TestRestoreCheckExplainsAnEmptyBucket(t *testing.T) {
	s := newSandbox(t)
	s.configureOffsite()
	s.tool("curl", `cat >/dev/null; printf '<ListBucketResult></ListBucketResult>'`)
	out, err := s.run("restore-check.sh", "--no-boot")
	if err == nil {
		t.Fatalf("restore check passed on an empty bucket:\n%s", out)
	}
	if !strings.Contains(out, "no backups found") {
		t.Fatalf("output = %q", out)
	}
}

func TestRestoreCheckAcceptsALocalEncryptedFileWithoutTheNetwork(t *testing.T) {
	s := newSandbox(t)
	s.configureOffsite()
	file := filepath.Join(s.home, "copy.db.gpg")
	s.write(file, "ENCRYPTED:SQLite format 3 fresh", 0o644)
	if out, err := s.run("restore-check.sh", "--no-boot", file); err != nil {
		t.Fatalf("restore check failed: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(s.home, "curl-args")); err == nil {
		t.Fatal("a local file reached the network")
	}
}

func (s *sandbox) assertTmpEmpty() {
	s.t.Helper()
	entries, err := os.ReadDir(filepath.Join(s.home, "tmp"))
	if err != nil {
		s.t.Fatal(err)
	}
	if len(entries) != 0 {
		s.t.Fatalf("left decrypted data behind in the temp dir: %v", entries)
	}
}
