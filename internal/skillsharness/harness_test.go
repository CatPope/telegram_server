package skillsharness_test

import (
	"os"
	"testing"

	"github.com/CatPope/telegram_server/internal/skillsharness"
)

func serverURL(t *testing.T) string {
	t.Helper()
	u := os.Getenv("TELEGRAM_SERVER_URL")
	if u == "" {
		t.Skip("TELEGRAM_SERVER_URL unset")
	}
	return u
}

func mustLoad(t *testing.T, skill string) skillsharness.Transcript {
	t.Helper()
	tr, err := skillsharness.LoadTranscript(skill)
	if err != nil {
		t.Fatalf("load transcript %q: %v", skill, err)
	}
	return tr
}

func TestSkillSendNotificationFixture(t *testing.T) {
	url := serverURL(t)
	tr := mustLoad(t, "send-notification")
	if err := skillsharness.RunFixture(t.Context(), tr, url); err != nil {
		t.Fatal(err)
	}
}

func TestSkillRegisterAppFixture(t *testing.T) {
	url := serverURL(t)
	tr := mustLoad(t, "register-app")
	if err := skillsharness.RunFixture(t.Context(), tr, url); err != nil {
		t.Fatal(err)
	}
}

func TestSkillManageUsersFixture(t *testing.T) {
	url := serverURL(t)
	tr := mustLoad(t, "manage-users")
	if err := skillsharness.RunFixture(t.Context(), tr, url); err != nil {
		t.Fatal(err)
	}
}

func TestSkillManageAppsFixture(t *testing.T) {
	url := serverURL(t)
	tr := mustLoad(t, "manage-apps")
	if err := skillsharness.RunFixture(t.Context(), tr, url); err != nil {
		t.Fatal(err)
	}
}

func TestSkillAuditSearchFixture(t *testing.T) {
	url := serverURL(t)
	tr := mustLoad(t, "audit-search")
	if err := skillsharness.RunFixture(t.Context(), tr, url); err != nil {
		t.Fatal(err)
	}
}

func TestSkillLiveSkipsWithoutAPIKey(t *testing.T) {
	if os.Getenv("CLAUDE_API_KEY") != "" {
		t.Skip("CLAUDE_API_KEY set; live test would need subprocess plumbing")
	}
	err := skillsharness.RunLive(t.Context(), skillsharness.Transcript{}, "")
	if err == nil {
		t.Fatal("expected error when CLAUDE_API_KEY unset")
	}
}
