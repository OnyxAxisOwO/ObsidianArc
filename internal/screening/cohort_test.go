package screening

import (
	"strings"
	"testing"
	"time"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

func TestDisabledSignupBatchOverridesAnIndividualApproval(t *testing.T) {
	facts := Facts{Username: "user35041", Email: "u977143@uberip.com"}
	for i, item := range [][2]string{
		{"user37854", "u491421@uberip.com"},
		{"user81904", "u869803@uberip.com"},
		{"user35640", "u556446@uberip.com"},
		{"user39369", "u544956@uberip.com"},
		{"user35910", "u882554@uberip.com"},
		{"user32616", "u205544@uberip.com"},
		{"user49414", "u474569@uberip.com"},
	} {
		facts.Recent = append(facts.Recent, RecentSignup{
			Username: item[0], Email: item[1],
			Status: user.StatusDisabled, APIRestricted: i < 5,
			APIRestrictionSource: "signup_review",
		})
	}
	cohort := cohortFor(facts)
	if cohort.matching != 7 || cohort.disabled != 7 || cohort.restricted != 5 || !cohort.confirmed() {
		t.Fatalf("cohort = %+v, want seven disabled matches", cohort)
	}
	if got := cohort.protect(Verdict{Decision: DecisionAllow}); got.Decision != DecisionRefuse {
		t.Fatalf("individual approval survived a confirmed batch: %+v", got)
	}
	if got := cohort.protectModel(Verdict{Decision: DecisionRestrict}); got.Decision != DecisionRefuse {
		t.Fatalf("confirmed batch was not refused after model review: %+v", got)
	}
	described := describe(facts)
	if !strings.Contains(described, "matching accounts disabled: 7") {
		t.Fatalf("reviewer did not receive the batch evidence: %s", described)
	}
	if strings.Contains(described, "u491421") || strings.Contains(described, "user37854") {
		t.Fatalf("another account's identity reached the model: %s", described)
	}
}

func TestACommonDomainOrNameShapeDoesNotRefuse(t *testing.T) {
	base := Facts{Username: "user35041", Email: "u977143@uberip.com"}
	base.Recent = []RecentSignup{
		{Username: "reader", Email: "person@uberip.com", Status: user.StatusDisabled},
		{Username: "user11111", Email: "u111111@example.com", Status: user.StatusDisabled},
		{Username: "user22222", Email: "other@uberip.com", Status: user.StatusDisabled},
	}
	cohort := cohortFor(base)
	if cohort.matching != 0 || cohort.confirmed() {
		t.Fatalf("independent weak signals were treated as a cohort: %+v", cohort)
	}
	if got := cohort.protect(Verdict{Decision: DecisionAllow}); got.Decision != DecisionAllow {
		t.Fatalf("ordinary registration was refused: %+v", got)
	}
	if got := cohort.protectModel(Verdict{Decision: DecisionRefuse, Reason: "EdgA is forged"}); got.Decision != DecisionRestrict {
		t.Fatalf("uncorroborated model refusal denied registration: %+v", got)
	}

	base.Recent = []RecentSignup{
		{Username: "user11111", Email: "u111111@uberip.com", Status: user.StatusActive},
		{Username: "user22222", Email: "u222222@uberip.com", Status: user.StatusActive},
		{Username: "user33333", Email: "u333333@uberip.com", Status: user.StatusActive},
	}
	cohort = cohortFor(base)
	if cohort.matching != 3 || cohort.confirmed() {
		t.Fatalf("three unreviewed peers were treated as a confirmed attack: %+v", cohort)
	}

	base.Recent = []RecentSignup{
		{Username: "user11111", Email: "u111111@uberip.com", Status: user.StatusDisabled, APIRestricted: true, APIRestrictionSource: "signup_review"},
		{Username: "user22222", Email: "u222222@uberip.com", Status: user.StatusDisabled, APIRestricted: true, APIRestrictionSource: "signup_review"},
		{Username: "user33333", Email: "u333333@uberip.com", Status: user.StatusDisabled},
		{Username: "user44444", Email: "u444444@uberip.com", Status: user.StatusActive},
		{Username: "user55555", Email: "u555555@uberip.com", Status: user.StatusActive},
	}
	cohort = cohortFor(base)
	if cohort.matching != 5 || cohort.confirmed() {
		t.Fatalf("a partially reviewed group was treated as confirmed abuse: %+v", cohort)
	}

	base.Recent[2].APIRestricted = true
	base.Recent[2].APIRestrictionSource = "signup_review"
	base.Recent[2].APIRestrictedUntil = time.Now().Add(-time.Hour).UnixMilli()
	base.Recent[3].Status = user.StatusDisabled
	base.Recent[3].APIRestricted = true
	base.Recent[3].APIRestrictionSource = "admin"
	cohort = cohortFor(base)
	if cohort.disabled != 4 || cohort.restricted != 2 || cohort.confirmed() {
		t.Fatalf("expired or manually imposed restrictions confirmed a batch: %+v", cohort)
	}
}

func TestQQNumberUsedAsHandleAndAddressIsNotACohortSignal(t *testing.T) {
	facts := Facts{Username: "123456789", Email: "123456789@qq.com"}
	for _, number := range []string{"987654321", "111222333", "444555666"} {
		facts.Recent = append(facts.Recent, RecentSignup{
			Username: number, Email: number + "@qq.com", Status: user.StatusDisabled,
		})
	}
	if cohort := cohortFor(facts); cohort.confirmed() || cohort.matching != 0 {
		t.Fatalf("QQ address convention was flagged as a bot cohort: %+v", cohort)
	}
}
