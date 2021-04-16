package common

import (
	"reflect"
	"strings"
	"time"

	"github.com/keikoproj/upgrade-manager/pkg/log"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

//All cluster level node upgrade statistics

var nodeRotationTotal = prometheus.NewHistogram(
	prometheus.HistogramOpts{
		Namespace: "node",
		Name:      "rotation_total_seconds",
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

var stepSummaries = make(map[string]map[string]prometheus.Summary)

func InitMetrics() {
	metrics.Registry.MustRegister(nodeRotationTotal)
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
					Namespace:   "node",
					Name:        stepName + "_seconds",
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
