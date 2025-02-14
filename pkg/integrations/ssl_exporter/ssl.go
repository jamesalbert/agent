package ssl_exporter

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/prometheus/client_golang/prometheus"
	ssl_config "github.com/ribbybibby/ssl_exporter/v2/config"
	"github.com/ribbybibby/ssl_exporter/v2/prober"
)

var (
	namespace  = "ssl"
	labelOrder = map[string]int{
		"prober":     0,
		"version":    0,
		"chain_no":   0,
		"file":       0,
		"namespace":  0,
		"kubeconfig": 0,
		"secret":     1,
		"name":       1,
		"key":        2,
		"type":       2,
		"serial_no":  3,
		"issuer_cn":  4,
		"cn":         5,
		"dnsnames":   6,
		"ips":        7,
		"emails":     8,
		"ou":         9,
	}
	descs = map[string]*prometheus.Desc{
		"ssl_exporter_probe_success": prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "probe_success"),
			"If the probe was a success",
			nil, nil,
		),
		"ssl_exporter_prober": prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "prober"),
			"The prober used by the exporter to connect to the target",
			[]string{"prober"}, nil,
		),
		"ssl_tls_version_info": prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "tls_version_info"),
			"The TLS version used",
			[]string{"version"}, nil,
		),
		"ssl_cert_not_after": prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "cert_not_after"),
			"NotAfter expressed as a Unix Epoch Time",
			[]string{"serial_no", "issuer_cn", "cn", "dnsnames", "ips", "emails", "ou"}, nil,
		),
		"ssl_cert_not_before": prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "cert_not_before"),
			"NotBefore expressed as a Unix Epoch Time",
			[]string{"serial_no", "issuer_cn", "cn", "dnsnames", "ips", "emails", "ou"}, nil,
		),
		"ssl_verified_cert_not_after": prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "verified_cert_not_after"),
			"NotAfter expressed as a Unix Epoch Time",
			[]string{"chain_no", "serial_no", "issuer_cn", "cn", "dnsnames", "ips", "emails", "ou"}, nil,
		),
		"ssl_verified_cert_not_before": prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "verified_cert_not_before"),
			"NotBefore expressed as a Unix Epoch Time",
			nil, nil,
		),
		"ssl_ocsp_response_stapled": prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "ocsp_response_stapled"),
			"If the connection state contains a stapled OCSP response",
			nil, nil,
		),
		"ssl_ocsp_response_status": prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "ocsp_response_status"),
			"The status in the OCSP response 0=Good 1=Revoked 2=Unknown",
			nil, nil,
		),
		"ssl_ocsp_response_produced_at": prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "ocsp_response_produced_at"),
			"The producedAt value in the OCSP response, expressed as a Unix Epoch Time",
			nil, nil,
		),
		"ssl_ocsp_response_this_update": prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "ocsp_response_this_update"),
			"The thisUpdate value in the OCSP response, expressed as a Unix Epoch Time",
			nil, nil,
		),
		"ssl_ocsp_response_next_update": prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "ocsp_response_next_update"),
			"The nextUpdate value in the OCSP response, expressed as a Unix Epoch Time",
			nil, nil,
		),
		"ssl_ocsp_response_revoked_at": prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "ocsp_response_revoked_at"),
			"The revocationTime value in the OCSP response, expressed as a Unix Epoch Time",
			nil, nil,
		),
		"ssl_file_cert_not_after": prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "file_cert_not_after"),
			"NotAfter expressed as a Unix Epoch Time for a certificate found in a file",
			[]string{"file", "serial_no", "issuer_cn", "cn", "dnsnames", "ips", "emails", "ou"}, nil,
		),
		"ssl_file_cert_not_before": prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "file_cert_not_before"),
			"NotBefore expressed as a Unix Epoch Time for a certificate found in a file",
			[]string{"file", "serial_no", "issuer_cn", "cn", "dnsnames", "ips", "emails", "ou"}, nil,
		),
		"ssl_kubernetes_cert_not_after": prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "kubernetes_cert_not_after"),
			"NotAfter expressed as a Unix Epoch Time for a certificate found in a kubernetes secret",
			[]string{"namespace", "secret", "key", "serial_no", "issuer_cn", "cn", "dnsnames", "ips", "emails", "ou"}, nil,
		),
		"ssl_kubernetes_cert_not_before": prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "kubernetes_cert_not_before"),
			"NotBefore expressed as a Unix Epoch Time for a certificate found in a kubernetes secret",
			[]string{"namespace", "secret", "key", "serial_no", "issuer_cn", "cn", "dnsnames", "ips", "emails", "ou"}, nil,
		),
		"ssl_kubeconfig_cert_not_after": prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "kubeconfig", "cert_not_after"),
			"NotAfter expressed as a Unix Epoch Time for a certificate found in a kubeconfig",
			[]string{"kubeconfig", "name", "type", "serial_no", "issuer_cn", "cn", "dnsnames", "ips", "emails", "ou"}, nil,
		),
		"ssl_kubeconfig_cert_not_before": prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "kubeconfig", "cert_not_before"),
			"NotBefore expressed as a Unix Epoch Time for a certificate found in a kubeconfig",
			[]string{"kubeconfig", "name", "type", "serial_no", "issuer_cn", "cn", "dnsnames", "ips", "emails", "ou"}, nil,
		),
	}
)

type Exporter struct {
	sync.Mutex
	probeSuccess prometheus.Gauge
	proberType   *prometheus.GaugeVec

	options   Options
	namespace string
}

type Options struct {
	Namespace   string
	MetricsPath string
	ProbePath   string
	Registry    *prometheus.Registry
	SSLTargets  []SSLTarget
	SSLConfig   *ssl_config.Config
	log         log.Logger
}

func NewSSLExporter(opts Options) (*Exporter, error) {
	e := &Exporter{
		options:   opts,
		namespace: opts.Namespace,
		probeSuccess: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: prometheus.BuildFQName(opts.Namespace, "", "probe_success"),
				Help: "If the probe was a success",
			},
		),
		proberType: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: prometheus.BuildFQName(opts.Namespace, "", "prober"),
				Help: "The prober used by the exporter to connect to the target",
			},
			[]string{"prober"},
		),
	}

	return e, nil
}

func (e *Exporter) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range descs {
		ch <- desc
	}
}

func (e *Exporter) Collect(ch chan<- prometheus.Metric) {
	e.Lock()
	defer e.Unlock()

	logger := e.options.log

	for _, target := range e.options.SSLTargets {
		ctx := context.Background()

		var moduleName string
		if target.Module != "" {
			moduleName = e.options.SSLConfig.DefaultModule
			if moduleName == "" {
				level.Error(logger).Log("msg", "Module parameter must be set")
				continue
			}
		}

		module, ok := e.options.SSLConfig.Modules[target.Module]
		if !ok {
			level.Error(logger).Log("msg", fmt.Sprintf("Unknown module '%s'", target.Module))
			continue
		}

		probeFunc, ok := prober.Probers[module.Prober]
		if !ok {
			level.Error(logger).Log("msg", fmt.Sprintf("Unknown prober %q", module.Prober))
			continue
		}

		e.options.Registry = prometheus.NewRegistry()
		e.options.Registry.MustRegister(e.probeSuccess, e.proberType)
		e.proberType.WithLabelValues(module.Prober).Set(1)

		// set high-level metric not collected in the prober
		err := probeFunc(ctx, logger, target.Target, module, e.options.Registry)
		if err != nil {
			level.Error(logger).Log("msg", err)
			e.probeSuccess.Set(0)
		} else {
			e.probeSuccess.Set(1)
		}

		// gather all the metrics we've collected in the prober
		metricFams, err := e.options.Registry.Gather()
		if err != nil {
			level.Error(logger).Log("msg", err)
			continue
		}
		for _, mf := range metricFams {
			for _, m := range mf.Metric {
				// get desc from name
				desc, ok := descs[*mf.Name]
				if !ok {
					level.Error(logger).Log("msg", fmt.Sprintf("Unknown metric %q", *mf.Name))
					continue
				}

				// ensure label order
				sort.Slice(m.Label, func(i, j int) bool {
					iPrec := labelOrder[*m.Label[i].Name]
					jPrec := labelOrder[*m.Label[j].Name]
					return iPrec < jPrec
				})
				labelValues := []string{}
				for _, l := range m.Label {
					labelValues = append(labelValues, *l.Value)
				}

				// create prometheus metric
				metric, err := prometheus.NewConstMetric(desc, prometheus.GaugeValue, *m.Gauge.Value, labelValues...)
				if err != nil {
					level.Error(logger).Log("msg", err)
					continue
				}
				ch <- metric
			}
		}
	}
}
