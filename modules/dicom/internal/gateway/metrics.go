package gateway

import "github.com/prometheus/client_golang/prometheus"

type metrics struct {
	connsTotal     prometheus.Counter
	connsActive    prometheus.Gauge
	stores         *prometheus.CounterVec // by result
	storeBytes     prometheus.Counter
	storeSeconds   prometheus.Histogram
	finds          *prometheus.CounterVec // by level, result
	findSeconds    prometheus.Histogram
	retrieveSubOps *prometheus.CounterVec // by op (move|get), result
	eventFailures  prometheus.Counter
}

func newMetrics(reg prometheus.Registerer) *metrics {
	m := &metrics{
		connsTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "rws_dicom_connections_total", Help: "DICOM TCP connections accepted.",
		}),
		connsActive: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "rws_dicom_connections_active", Help: "DICOM TCP connections currently open.",
		}),
		stores: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "rws_dicom_cstore_total", Help: "C-STORE requests handled, by result.",
		}, []string{"result"}),
		storeBytes: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "rws_dicom_cstore_bytes_total", Help: "Bytes archived via C-STORE.",
		}),
		storeSeconds: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name: "rws_dicom_cstore_seconds", Help: "Time to archive, index and publish one instance.",
			Buckets: prometheus.ExponentialBuckets(0.002, 2, 14),
		}),
		finds: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "rws_dicom_cfind_total", Help: "C-FIND requests handled, by level and result.",
		}, []string{"level", "result"}),
		findSeconds: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name: "rws_dicom_cfind_seconds", Help: "C-FIND query time.",
			Buckets: prometheus.ExponentialBuckets(0.001, 2, 14),
		}),
		retrieveSubOps: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "rws_dicom_retrieve_subops_total", Help: "C-MOVE/C-GET sub-operations, by op and result.",
		}, []string{"op", "result"}),
		eventFailures: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "rws_dicom_event_publish_failures_total", Help: "Events that failed to publish.",
		}),
	}
	reg.MustRegister(m.connsTotal, m.connsActive, m.stores, m.storeBytes, m.storeSeconds,
		m.finds, m.findSeconds, m.retrieveSubOps, m.eventFailures)
	return m
}
