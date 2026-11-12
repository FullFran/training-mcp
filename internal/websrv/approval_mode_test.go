package websrv_test

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/fullfran/training-mcp/internal/training"
)

var approvalModes = []string{training.ApprovalReviewAll, training.ApprovalAcceptExceptDelete, training.ApprovalAcceptAll}

// radio returns the <input> tag of the mode option and whether it is checked.
func radio(t *testing.T, body, mode string) (found, checked bool) {
	t.Helper()
	m := regexp.MustCompile(`<input[^>]*name="mode"[^>]*value="` + mode + `"[^>]*>`).FindString(body)
	return m != "", strings.Contains(m, "checked")
}

func TestReviewPageShowsTheThreeModesWithTheCurrentOneSelected(t *testing.T) {
	f := newReviewFixture(t) // review_all
	for _, current := range approvalModes {
		if err := f.service.SetApprovalMode(context.Background(), current); err != nil {
			t.Fatal(err)
		}
		body := get(t, f.handler, "/review").Body.String()
		for _, mode := range approvalModes {
			found, checked := radio(t, body, mode)
			if !found || checked != (mode == current) {
				t.Errorf("current %s: option %s found=%v checked=%v", current, mode, found, checked)
			}
		}
		for _, label := range []string{"Revisar todo", "Aceptar todo salvo borrar", "Aceptar todo"} {
			if !strings.Contains(body, label) {
				t.Errorf("no label %q", label)
			}
		}
		if !strings.Contains(body, `action="/review/mode"`) || !strings.Contains(body, `name="csrf_token" value="tok"`) {
			t.Errorf("the form lacks its action or the CSRF field:\n%s", body)
		}
	}
}

func TestANewAccountShowsAcceptExceptDeleteSelected(t *testing.T) {
	f := newReviewFixture(t)
	if err := f.service.SetApprovalMode(context.Background(), training.DefaultApprovalMode); err != nil {
		t.Fatal(err)
	}
	if _, checked := radio(t, get(t, f.handler, "/review").Body.String(), training.ApprovalAcceptExceptDelete); !checked {
		t.Fatal("the default is not selected")
	}
}

func TestPostingAModeChangesIt(t *testing.T) {
	f := newReviewFixture(t)
	for _, mode := range approvalModes {
		if w := post(t, f.handler, "/review/mode", url.Values{"mode": {mode}}, false); w.Code != http.StatusSeeOther {
			t.Fatalf("%s: status %d", mode, w.Code)
		}
		if perms, _ := f.service.Permissions(context.Background()); perms.Mode != mode {
			t.Fatalf("mode = %q, want %s", perms.Mode, mode)
		}
	}
}

func TestAnInvalidModeIsRefusedAndChangesNothing(t *testing.T) {
	f := newReviewFixture(t)
	for _, bad := range []string{"", "accept-all", "ACCEPT_ALL", "yes"} {
		w := post(t, f.handler, "/review/mode", url.Values{"mode": {bad}}, false)
		if w.Code != http.StatusBadRequest {
			t.Errorf("mode %q: status %d", bad, w.Code)
		}
	}
	if perms, _ := f.service.Permissions(context.Background()); perms.Mode != training.ApprovalReviewAll {
		t.Fatalf("mode changed to %q", perms.Mode)
	}
}

// The old route is gone: nothing else can flip accept-all.
func TestTheAcceptAllSwitchRouteIsGone(t *testing.T) {
	f := newReviewFixture(t)
	if w := post(t, f.handler, "/review/accept-all", url.Values{"on": {"1"}}, false); w.Code == http.StatusSeeOther {
		t.Fatalf("status %d", w.Code)
	}
	if perms, _ := f.service.Permissions(context.Background()); perms.Mode != training.ApprovalReviewAll {
		t.Fatalf("mode = %q", perms.Mode)
	}
}

func TestAnAutoAcceptedProposalShowsHowItWasDecided(t *testing.T) {
	f := newReviewFixture(t)
	if err := f.service.SetApprovalMode(context.Background(), training.ApprovalAcceptExceptDelete); err != nil {
		t.Fatal(err)
	}
	if p := f.propose(t, "pierna"); p.Status != training.ProposalAccepted || p.DecidedBy != training.DecidedByAcceptExceptDelete {
		t.Fatalf("proposal = %+v", p)
	}
	if body := get(t, f.handler, "/review").Body.String(); !strings.Contains(body, "aceptar todo salvo borrar") {
		t.Fatalf("the history does not say how it was decided:\n%s", body)
	}
}
