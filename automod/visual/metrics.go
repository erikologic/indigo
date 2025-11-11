package visual

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var hiveAPIDuration = promauto.NewHistogram(prometheus.HistogramOpts{
	Name: "automod_hive_api_duration_sec",
	Help: "Duration of Hive image auto-labeling API calls",
})

var hiveAPICount = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "automod_hive_api_count",
	Help: "Number of Hive image auto-labeling API calls, by HTTP status code",
}, []string{"status"})

var abyssAPIDuration = promauto.NewHistogram(prometheus.HistogramOpts{
	Name: "automod_abyss_api_duration_sec",
	Help: "Duration of abyss image scanning API call",
})

var abyssAPICount = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "automod_abyss_api_count",
	Help: "Number of abyss image scanning API calls, by HTTP status code",
}, []string{"status"})

var csamAPIDuration = promauto.NewHistogram(prometheus.HistogramOpts{
	Name: "automod_csam_api_duration_sec",
	Help: "Duration of CSAM detection API calls",
})

var csamAPICount = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "automod_csam_api_count",
	Help: "Number of CSAM detection API calls, by HTTP status code",
}, []string{"status"})

var spamHashCheckDuration = promauto.NewHistogram(prometheus.HistogramOpts{
	Name: "automod_spam_hash_check_duration_sec",
	Help: "Duration of spam perceptual hash check operations",
})

var spamHashCheckCount = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "automod_spam_hash_check_count",
	Help: "Number of spam hash checks performed, by result (spam/clean/error)",
}, []string{"result"})
