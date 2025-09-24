package config

// Canonical realm names aligned with PracticingData information architecture
// These realms represent the supported data domains for Sumpter processing

const (
	// RealmRetail represents retail/e-commerce data sources
	RealmRetail = "retail"

	// RealmFinance represents financial data sources (SEC EDGAR, etc.)
	RealmFinance = "finance"

	// RealmHealthcare represents healthcare data sources
	RealmHealthcare = "healthcare"

	// RealmEnvironment represents environmental data sources
	RealmEnvironment = "environment"

	// RealmLegal represents legal data sources
	RealmLegal = "legal"

	// RealmGeneral represents general-purpose data sources
	RealmGeneral = "general"
)

// ValidRealms returns the list of all valid realm names
func ValidRealms() []string {
	return []string{
		RealmRetail,
		RealmFinance,
		RealmHealthcare,
		RealmEnvironment,
		RealmLegal,
		RealmGeneral,
	}
}

// IsValidRealm checks if a given realm name is valid
func IsValidRealm(realm string) bool {
	validRealms := ValidRealms()
	for _, validRealm := range validRealms {
		if realm == validRealm {
			return true
		}
	}
	return false
}
