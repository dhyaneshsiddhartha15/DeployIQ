package model

import "time"

// Role is a member's permission level in an organisation. Two roles only:
// Phase 9.3 rules out a general RBAC system, and "can manage the org" versus
// "can see the org" is the distinction teams actually need.
type Role string

const (
	// RoleAdmin manages members, invitations and settings.
	RoleAdmin Role = "admin"
	// RoleMember reads the organisation's data and reports into it.
	RoleMember Role = "member"
)

// Valid reports whether r is a known role.
func (r Role) Valid() bool { return r == RoleAdmin || r == RoleMember }

// Organization groups users so a team sees each other's results (Phase 13.1:
// team accounts). Backs the `organizations` collection.
type Organization struct {
	ID        string    `json:"id"`
	Slug      string    `json:"slug"` // URL name, unique: "acme-platform"
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`

	// Settings are editable by admins.
	Settings OrgSettings `json:"settings"`
}

// OrgSettings parameterise the cost estimate. Defaults are typical public
// cloud list prices; every company's are different, which is why they are
// settings rather than constants.
type OrgSettings struct {
	// StoragePerGBMonthUSD is registry storage cost per GB-month (ECR,
	// GCR/Artifact Registry and ACR are all around $0.10).
	StoragePerGBMonthUSD float64 `json:"storagePerGBMonthUSD"`

	// TransferPerGBUSD is the cost of moving one GB out of the registry
	// (cross-region or internet egress; $0 for same-region pulls).
	TransferPerGBUSD float64 `json:"transferPerGBUSD"`

	// PullsPerDay is how often a typical image is pulled (CI runs, deploys,
	// autoscaling nodes).
	PullsPerDay float64 `json:"pullsPerDay"`

	// VersionsKept is how many image versions the registry retains per repo:
	// storage is paid for every one of them.
	VersionsKept float64 `json:"versionsKept"`
}

// DefaultOrgSettings are used for a new organisation.
func DefaultOrgSettings() OrgSettings {
	return OrgSettings{StoragePerGBMonthUSD: 0.10, TransferPerGBUSD: 0.09, PullsPerDay: 20, VersionsKept: 30}
}

// MonthlySavingUSD estimates what savedMB per repository is worth per month:
// storage for every retained version plus transfer for every pull.
func (s OrgSettings) MonthlySavingUSD(savedMB float64) float64 {
	gb := savedMB / 1024
	return gb*s.VersionsKept*s.StoragePerGBMonthUSD + gb*s.PullsPerDay*30*s.TransferPerGBUSD
}

// Membership links a user to an organisation. Backs `memberships`.
type Membership struct {
	OrgID     string    `json:"orgId"`
	UserID    string    `json:"userId"`
	Role      Role      `json:"role"`
	CreatedAt time.Time `json:"createdAt"`
}

// Invitation lets an admin add someone who has not logged in yet. It is
// claimed automatically when a user with that email (or GitHub login) signs
// in. Backs `invitations`.
type Invitation struct {
	ID        string    `json:"id"`
	OrgID     string    `json:"orgId"`
	Email     string    `json:"email,omitempty"`
	Login     string    `json:"login,omitempty"`
	Role      Role      `json:"role"`
	InvitedBy string    `json:"invitedBy"`
	CreatedAt time.Time `json:"createdAt"`
}

// Member is a membership joined with the user, for the members page.
type Member struct {
	User User `json:"user"`
	Role Role `json:"role"`
}

// RepoSummary is one repository's latest result in an organisation.
type RepoSummary struct {
	RepoName        string       `json:"repoName"`
	Latest          Optimization `json:"latest"`
	Runs            int          `json:"runs"`
	ReportedBy      string       `json:"reportedBy"` // login or email of the latest reporter
	MonthlySavedUSD float64      `json:"monthlySavedUSD"`
}

// OrgSummary is the organisation dashboard's headline view.
type OrgSummary struct {
	Org             Organization  `json:"org"`
	Repos           []RepoSummary `json:"repos"`
	Runs            int           `json:"runs"`
	CurrentMB       float64       `json:"currentMB"`
	OptimizedMB     float64       `json:"optimizedMB"`
	SavedMB         float64       `json:"savedMB"`
	AvgSavedPercent float64       `json:"avgSavedPercent"`
	MonthlySavedUSD float64       `json:"monthlySavedUSD"`
	Members         int           `json:"members"`
}
