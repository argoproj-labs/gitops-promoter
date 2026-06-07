package metrics

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

var _ = Describe("PromoterNotification delivery metrics", func() {
	It("records delivered, failed, and retry counters per namespace/name/event_type", func() {
		ns, name, eventType := "notif-metrics-ns", "notif-a", "PromotionComplete"

		RecordPromoterNotificationDelivered(ns, name, eventType)
		RecordPromoterNotificationDelivered(ns, name, eventType)
		Expect(testutil.ToFloat64(promoterNotificationsDeliveredTotal.WithLabelValues(ns, name, eventType))).To(Equal(2.0))

		RecordPromoterNotificationFailed(ns, name, eventType)
		Expect(testutil.ToFloat64(promoterNotificationsFailedTotal.WithLabelValues(ns, name, eventType))).To(Equal(1.0))

		RecordPromoterNotificationRetry(ns, name, eventType)
		RecordPromoterNotificationRetry(ns, name, eventType)
		RecordPromoterNotificationRetry(ns, name, eventType)
		Expect(testutil.ToFloat64(promoterNotificationsRetryTotal.WithLabelValues(ns, name, eventType))).To(Equal(3.0))

		// Distinct event_type label is a separate series.
		RecordPromoterNotificationDelivered(ns, name, "GateFailed")
		Expect(testutil.ToFloat64(promoterNotificationsDeliveredTotal.WithLabelValues(ns, name, "GateFailed"))).To(Equal(1.0))
	})
})
