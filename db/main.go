package main

import (
	"crypto/md5"
	"fmt"
	"log"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/felixge/httpsnoop"
	kitlog "github.com/go-kit/kit/log"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	dbPort = ":80"

	failPercent = 10
)

var (
	requestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "request_duration_seconds",
		Help:    "Time (in seconds) spent serving HTTP requests",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "route", "status_code"})

	logger = kitlog.NewLogfmtLogger(kitlog.NewSyncWriter(os.Stderr))

	fail = false
)

func main() {
	rand.Seed(time.Now().UnixNano())

	peers := getPeers()
	logger.Log("msg", "peer(s)", "num", len(peers))

	h := md5.New()
	fmt.Fprintf(h, "%d", rand.Int63())
	id := fmt.Sprintf("%x", h.Sum(nil))

	http.HandleFunc("/fail", func(w http.ResponseWriter, r *http.Request) {
		fail = !fail
	})
	http.Handle("/metrics", promhttp.Handler())
	http.HandleFunc("/", wrap(func(w http.ResponseWriter, r *http.Request) {
		// Randomly fail x% of the requests.
		if fail {
			if rand.Intn(100) <= failPercent {
				time.Sleep(1 * time.Second)
				logger.Log("error", "query lock timeout")
				w.WriteHeader(http.StatusInternalServerError)

				return
			}
		}

		fmt.Fprintf(w, "db-%s OK\n", id)
	}))

	errc := make(chan error)
	go func() { errc <- http.ListenAndServe(dbPort, nil) }()
	go func() { errc <- interrupt() }()
	log.Fatal(<-errc)
}

func interrupt() error {
	c := make(chan os.Signal)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
	return fmt.Errorf("%s", <-c)
}

func id() string {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown-host"
	}
	return hostname
}

func getPeers() []*url.URL {
	peers := []*url.URL{}
	for _, host := range os.Args[1:] {
		if _, _, err := net.SplitHostPort(host); err != nil {
			host = host + dbPort
		}
		u, err := url.Parse(fmt.Sprintf("http://%s", host))
		if err != nil {
			log.Fatal(err)
		}
		logger.Log("peer", u.String())
		peers = append(peers, u)
	}

	return peers
}

func wrap(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m := httpsnoop.CaptureMetrics(h, w, r)
		requestDuration.WithLabelValues(r.Method, r.URL.Path, strconv.Itoa(m.Code)).Observe(m.Duration.Seconds())
	}
}
