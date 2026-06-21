package strategy

import (
	"context"
	"errors"
	"testing"
)

type fakeTopicResolver struct {
	gotApp string
	gotMin string
	res    ResolveResult
	err    error
}

func (f *fakeTopicResolver) ResolveTopic(_ context.Context, appID, minGrade string) (ResolveResult, error) {
	f.gotApp = appID
	f.gotMin = minGrade
	return f.res, f.err
}

func TestTopicStrategyRejectsEmptyApp(t *testing.T) {
	s := &TopicStrategy{Resolver: &fakeTopicResolver{}}
	_, err := s.Resolve(context.Background(), Request{})
	if !errors.Is(err, ErrAppNotFound) {
		t.Fatalf("expected ErrAppNotFound, got %v", err)
	}
}

func TestTopicStrategyForwardsMinGradeAndApp(t *testing.T) {
	f := &fakeTopicResolver{res: ResolveResult{Recipients: []RecipientHandle{{UserID: 1}}}}
	s := &TopicStrategy{Resolver: f}
	if _, err := s.Resolve(context.Background(), Request{AppID: "alerts", MinGrade: "admin"}); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if f.gotApp != "alerts" || f.gotMin != "admin" {
		t.Fatalf("forwarded values mismatch: app=%q grade=%q", f.gotApp, f.gotMin)
	}
}

func TestTopicStrategyDefaultsMinGradeToUser(t *testing.T) {
	f := &fakeTopicResolver{}
	s := &TopicStrategy{Resolver: f}
	if _, err := s.Resolve(context.Background(), Request{AppID: "x"}); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if f.gotMin != "user" {
		t.Fatalf("expected default min_grade=user, got %q", f.gotMin)
	}
}

func TestTopicStrategyName(t *testing.T) {
	if (&TopicStrategy{}).Name() != "topic" {
		t.Fatal("name drift")
	}
}

func TestGradeRankUnknownFailsClosed(t *testing.T) {
	// Regression guard: empty/unknown grade strings must rank 0 so a stored
	// grade of "" or "superuser" never satisfies any minGrade ('user' or above).
	if r := gradeRank(""); r != 0 {
		t.Fatalf("gradeRank(\"\")=%d, want 0 (fail-closed)", r)
	}
	if r := gradeRank("superuser"); r != 0 {
		t.Fatalf("gradeRank(\"superuser\")=%d, want 0", r)
	}
	if gradeRank("") >= gradeRank("user") {
		t.Fatal("empty grade must NOT satisfy minGrade=user (fail-closed)")
	}
}

func TestGradeRankOrdering(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"admin", "user", true},
		{"admin", "developer", true},
		{"developer", "user", true},
		{"user", "user", true},
		{"user", "admin", false},
		{"developer", "admin", false},
	}
	for _, c := range cases {
		got := gradeRank(c.a) >= gradeRank(c.b)
		if got != c.want {
			t.Errorf("gradeRank(%q) >= gradeRank(%q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestMaxGradeReturnsHigher(t *testing.T) {
	if maxGrade("user", "admin") != "admin" {
		t.Fatal("max(user,admin) should be admin")
	}
	if maxGrade("admin", "user") != "admin" {
		t.Fatal("max(admin,user) should be admin")
	}
	if maxGrade("developer", "developer") != "developer" {
		t.Fatal("max(developer,developer) should be developer")
	}
}

func TestNormalizeGradeAliases(t *testing.T) {
	if normalizeGrade("DEV") != "developer" {
		t.Fatal("DEV should normalize to developer")
	}
	if normalizeGrade("") != "user" {
		t.Fatal("empty should default to user")
	}
	if normalizeGrade("  Admin  ") != "admin" {
		t.Fatal("admin should be trimmed and lowercased")
	}
}
