package common

import (
	"github.com/keikoproj/upgrade-manager/controllers/common/log"
	"github.com/prometheus/client_golang/prometheus"
	"reflect"
	"strings"
	"time"
)

//All cluster level node upgrade statistics

var nodeRotationTotal = prometheus.NewHistogram(
	prometheus.HistogramOpts{
		Namespace: "upgrade-manager",
		Name:      "node_rotation_total_seconds",
		Help:      "Node rotation total",
	})

var stepSummaries = make(map[string]map[string]prometheus.Summary)

func Init() {
	prometheus.MustRegister(nodeRotationTotal)
}

// Add rolling update step duration when the step is completed
func AddRollingUpgradeStepDuration(asgName string, stepName string, duration time.Duration) {
	if strings.EqualFold(stepName, "total") { //Histogram
		nodeRotationTotal.Observe(duration.Seconds())
	} else { //Summary
		var steps map[string]prometheus.Summary
		if m, ok := stepSummaries[asgName]; !ok {
			steps = make(map[string]prometheus.Summary)
			stepSummaries[asgName] = steps
		} else {
			steps = m
		}

		var summary prometheus.Summary
		if s, ok := steps[stepName]; !ok {
			summary = prometheus.NewSummary(
				prometheus.SummaryOpts{
					Namespace: asgName,
					Name:      "node_" + stepName + "_seconds",
					Help:      "Summary for node " + stepName,
				})
			err := prometheus.Register(summary)
			if reflect.TypeOf(err).String() == "AlreadyRegisteredError" {
				log.Warnf("summary was registered again, ASG: %s, step: %s", asgName, stepName)
			} else {
				log.Errorf("register summary error, ASG: %s, step: %s, %v", asgName, stepName, err)
			}
			steps[stepName] = summary
		} else {
			summary = s
		}
		summary.Observe(duration.Seconds())
	}
}
