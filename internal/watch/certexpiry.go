package watch

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"skifity/internal/notify"
	"skifity/internal/store"
	"skifity/internal/tlscert"
)

// A certificate a team brought does not renew itself. Let's Encrypt's do, and
// that is the whole reason people forget theirs: nothing about the panel says
// a date is coming until browsers start refusing the site.
//
// So each one is warned about 21, 7 and 1 days before it runs out — once per
// threshold, which is recorded on the certificate, so a restart or a check
// that runs more often sends nothing twice. Uploading the renewal under the
// same name starts the count again.
//
// It needs no cluster, so it is not part of the watcher's pass, which does not
// run without one; the minute tick calls it.

// CertificateStore is the part of the database the expiry check uses.
type CertificateStore interface {
	ListAllCertificates(ctx context.Context) ([]store.Certificate, error)
	SetCertificateExpiryNotified(ctx context.Context, id string, notAfter time.Time, days int) error
}

// CheckCertificateExpiry sends every warning that is due now, and records it.
func CheckCertificateExpiry(ctx context.Context, db CertificateStore, notifier notify.Notifier, now time.Time, log *slog.Logger) {
	if notifier == nil {
		return
	}
	certificates, err := db.ListAllCertificates(ctx)
	if err != nil {
		log.Warn("could not list certificates for their expiry dates", "error", err)
		return
	}
	for _, certificate := range certificates {
		days, due := tlscert.DueThreshold(certificate.NotAfter, now, certificate.ExpiryNotified)
		if !due {
			continue
		}
		// Recorded before it is sent: a warning that could not be recorded
		// would be sent again every hour, which is worse than one that was
		// recorded and then lost on its way.
		if err := db.SetCertificateExpiryNotified(ctx, certificate.ID, certificate.NotAfter, days); err != nil {
			log.Warn("could not record a certificate's expiry warning", "certificate", certificate.ID, "error", err)
			continue
		}
		// No ProjectID, on purpose: a certificate is the team's and covers
		// hostnames in any of its projects, so this goes to every channel
		// that takes the event. See notify.EventCertificateExpiring.
		notifier.Notify(ctx, certificate.TeamID, notify.EventCertificateExpiring, expiryMessage(certificate, now))
	}
}

// expiryMessage is the warning for one certificate.
func expiryMessage(certificate store.Certificate, now time.Time) notify.Message {
	left := certificate.NotAfter.Sub(now)
	date := certificate.NotAfter.UTC().Format("2 January 2006, 15:04 UTC")
	// Whole days, rounded up: 20 days and an hour is "21 days", which is the
	// threshold it was sent for.
	days := int((left + 24*time.Hour - 1) / (24 * time.Hour))
	title := fmt.Sprintf("The certificate %s expires in %d days", certificate.Name, days)
	level := "warning"
	switch {
	case left <= 0:
		title = fmt.Sprintf("The certificate %s has expired", certificate.Name)
		level = "error"
	case left <= 24*time.Hour:
		title = fmt.Sprintf("The certificate %s expires within a day", certificate.Name)
		level = "error"
	}
	return notify.Message{
		Title: title,
		Body: fmt.Sprintf("It runs out on %s, and browsers refuse every hostname it covers from then on. "+
			"Renew it with the authority that issued it, and upload the new one under the same name in "+
			"Settings, Certificates: every app using it is updated.", date),
		Level: level,
		Path:  "/settings?tab=certificates",
		Fields: map[string]string{
			"Certificate": certificate.Name,
			"Expires":     date,
			"Hostnames":   strings.Join(certificate.Hostnames, ", "),
			"Issuer":      certificate.Issuer,
		},
	}
}
