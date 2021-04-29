package common

import (
	"reflect"
	"strings"
	"time"

	"github.com/keikoproj/upgrade-manager/controllers/common/log"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	metricNamespace = "upgrade_manager_v2"

	//All cluster level node upgrade statistics
	nodeRotationTotal = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: metricNamespace,
			Name:      "node_rotation_total_seconds",
			Help:      "Node rotation total",
			Buckets: []float64{
				10.0,
				30.0,
				60.0,
				90.0,
				120.0,
				180.0,
				300.0,
				600.0,
				900.0,
			},
		})

	stepSummaries = make(map[string]map[string]prometheus.Summary)

	CRStatus = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: metricNamespace,
			Name:      "resource_status",
			Help:      "Rollup CR statistics, partitioned by name.",
		},
		[]string{
			// name of the CR
			"resource_name",
		},
	)
)

func InitMetrics() {
	metrics.Registry.MustRegister(nodeRotationTotal)
	metrics.Registry.MustRegister(CRStatus)
}

// Add rolling update step duration when the step is completed
func AddStepDuration(groupName string, stepName string, duration time.Duration) {
	if strings.EqualFold(stepName, "total") { //Histogram
		nodeRotationTotal.Observe(duration.Seconds())
	} else { //Summary
		var steps map[string]prometheus.Summary
		if m, ok := stepSummaries[groupName]; !ok {
			steps = make(map[string]prometheus.Summary)
			stepSummaries[groupName] = steps
		} else {
			steps = m
		}

		var summary prometheus.Summary
		if s, ok := steps[stepName]; !ok {
			summary = prometheus.NewSummary(
				prometheus.SummaryOpts{
					Namespace:   metricNamespace,
					Name:        "node_" + stepName + "_seconds",
					Help:        "Summary for node " + stepName,
					ConstLabels: prometheus.Labels{"group": groupName},
				})
			err := metrics.Registry.Register(summary)
			if err != nil {
				if reflect.TypeOf(err).String() == "prometheus.AlreadyRegisteredError" {
					log.Warnf("summary was registered again, group: %s, step: %s", groupName, stepName)
				} else {
					log.Errorf("register summary error, group: %s, step: %s, %v", groupName, stepName, err)
				}
			}
			steps[stepName] = summary
		} else {
			summary = s
		}
		summary.Observe(duration.Seconds())
	}
}

func SetMetricRollupInitOrRunning(ruName string) {
	CRStatus.WithLabelValues(ruName).Set(0)
}

func SetMetricRollupCompleted(ruName string) {
	CRStatus.WithLabelValues(ruName).Set(1)
}

func SetMetricRollupFailed(ruName string) {
	CRStatus.WithLabelValues(ruName).Set(-1)
}
