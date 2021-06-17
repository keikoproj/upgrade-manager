package common

import (
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/keikoproj/upgrade-manager/pkg/log"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	metricNamespace = "upgrade_manager"

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

	totalProcessingTime = make(map[string]prometheus.Summary)

	totalNodesMetrics = make(map[string]prometheus.Gauge)

	nodesProcessedMetrics = make(map[string]prometheus.Gauge)

	stepSummaries = make(map[string]map[string]prometheus.Summary)

	stepSumMutex = sync.Mutex{}

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

//Observe total processing time
func TotalProcessingTime(groupName string, duration time.Duration) {
	var summary prometheus.Summary
	if s, ok := totalProcessingTime[groupName]; !ok {
		summary = prometheus.NewSummary(
			prometheus.SummaryOpts{
				Namespace:   metricNamespace,
				Name:        "total_processing_time_seconds",
				Help:        "Total processing time for all nodes in the group",
				ConstLabels: prometheus.Labels{"group": groupName},
			})
		err := metrics.Registry.Register(summary)
		if err != nil {
			if reflect.TypeOf(err).String() == "prometheus.AlreadyRegisteredError" {
				log.Warnf("summary was registered again, group: %s", groupName)
			} else {
				log.Errorf("register summary error, group: %s, %v", groupName, err)
			}
		}
		stepSumMutex.Lock()
		totalProcessingTime[groupName] = summary
		stepSumMutex.Unlock()
	} else {
		summary = s
	}
	summary.Observe(duration.Seconds())
}

func addGaugeOrUpdate(gaugeMap map[string]prometheus.Gauge, groupName string, count int, metricName, help string) {
	var gauge prometheus.Gauge
	if c, ok := gaugeMap[groupName]; !ok {
		gauge = prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace:   metricNamespace,
				Name:        metricName,
				Help:        help,
				ConstLabels: prometheus.Labels{"group": groupName},
			})
		err := metrics.Registry.Register(gauge)
		if err != nil {
			if reflect.TypeOf(err).String() == "prometheus.AlreadyRegisteredError" {
				log.Warnf("gauge was registered again, group: %s", groupName)
			} else {
				log.Errorf("register gauge error, group: %s, %v", groupName, err)
			}
		}
		stepSumMutex.Lock()
		gaugeMap[groupName] = gauge
		stepSumMutex.Unlock()
	} else {
		gauge = c
	}
	gauge.Set(float64(count))
}

func SetTotalNodesMetric(groupName string, nodes int) {
	addGaugeOrUpdate(totalNodesMetrics, groupName, nodes, "total_nodes", "Total nodes in the group")
}

func SetNodesProcessedMetric(groupName string, nodesProcessed int) {
	addGaugeOrUpdate(nodesProcessedMetrics, groupName, nodesProcessed, "nodes_processed", "Nodes processed in the group")
}

// Add rolling update step duration when the step is completed
func AddStepDuration(groupName string, stepName string, duration time.Duration) {
	if strings.EqualFold(stepName, "total") { //Histogram
		nodeRotationTotal.Observe(duration.Seconds())
	} else { //Summary
		var steps map[string]prometheus.Summary
		if m, ok := stepSummaries[groupName]; !ok {
			steps = make(map[string]prometheus.Summary)
			stepSumMutex.Lock()
			stepSummaries[groupName] = steps
			stepSumMutex.Unlock()
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
			stepSumMutex.Lock()
			steps[stepName] = summary
			stepSumMutex.Unlock()
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
