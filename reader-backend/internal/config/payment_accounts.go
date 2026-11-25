package config

import "os"

// PaymentAccount points to environment variables injected for a registered
// merchant. The reference list contains no credential values.
type PaymentAccount struct {
	Provider    string `json:"provider"`
	Account     string `json:"account"`
	Environment string `json:"environment"`
	SecretEnv   string `json:"secret_env"`
	WebhookEnv  string `json:"webhook_env"`
	MerchantEnv string `json:"merchant_env"`
	LedgerEnv   string `json:"ledger_env"`
	Primary     bool   `json:"primary"`
}

func FindPaymentAccount(provider, account, environment string) (PaymentAccount, bool) {
	if AppConfig != nil {
		for _, reference := range AppConfig.PaymentAccounts {
			if reference.Provider == provider && reference.Account == account && reference.Environment == environment {
				return reference, true
			}
		}
	}
	return PaymentAccount{}, false
}

func StripeLedgerAPIKey(account, environment string) string {
	if ref, ok := FindPaymentAccount("stripe", account, environment); ok {
		if ref.LedgerEnv != "" {
			return os.Getenv(ref.LedgerEnv)
		}
		return os.Getenv(ref.SecretEnv)
	}
	return os.Getenv("STRIPE_LEDGER_API_KEY")
}

func StripeAftercareAPIKey(account, environment string) string {
	if ref, ok := FindPaymentAccount("stripe", account, environment); ok {
		return os.Getenv(ref.SecretEnv)
	}
	return os.Getenv("STRIPE_AFTERCARE_API_KEY")
}
