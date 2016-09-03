package main

import (
	"doppler/dopplerservice"
	"doppler/sinks/retrystrategy"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/exec"
	"time"

	"logger"
	"signalmanager"

	"metron/backoff"
	"metron/clientpool"
	"metron/config"
	"metron/eventwriter"
	"metron/networkreader"
	"metron/writers/dopplerforwarder"
	"metron/writers/eventmarshaller"
	"metron/writers/eventunmarshaller"
	"metron/writers/messageaggregator"
	"metron/writers/tagger"

	"github.com/cloudfoundry/dropsonde/metric_sender"
	"github.com/cloudfoundry/dropsonde/metricbatcher"
	"github.com/cloudfoundry/dropsonde/metrics"
	"github.com/cloudfoundry/dropsonde/runtime_stats"
	"github.com/cloudfoundry/gosteno"
	"github.com/cloudfoundry/gunk/workpool"
	"github.com/cloudfoundry/sonde-go/events"
	"github.com/cloudfoundry/storeadapter"
	"github.com/cloudfoundry/storeadapter/etcdstoreadapter"
	"github.com/gogo/protobuf/proto"
)

const (
	// This is 6061 to not conflict with any other jobs that might have pprof
	// running on 6060
	pprofPort         = "6061"
	origin            = "MetronAgent"
	connectionRetries = 15
	NodeKey           = "/doppler/meta/z1/doppler_z1/0"
)

var defaultConfig = mustParseConfig()

func mustParseConfig() *config.Config {
	conf, err := config.ParseConfig("./metron.json")
	if err != nil {
		panic(err)
	}
	return conf
}

func resetETCD() {
	const path = "http://localhost:4001/v2/keys/doppler?recursive=true"
	if err := exec.Command("curl", "-X", "DELETE", path).Run(); err != nil {
		panic(err)
	}
}

func init() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	resetETCD()
}

func StartMetron(config *config.Config, reader *io.PipeReader) error {
	// Metron is intended to be light-weight so we occupy only one core
	// WARN
	// runtime.GOMAXPROCS(1)

	logger := logger.NewLogger(false, os.DevNull, "metron", config.Syslog)

	statsStopChan := make(chan struct{})
	batcher, eventWriter := initializeMetrics(config, statsStopChan, logger)

	go func() {
		err := http.ListenAndServe(net.JoinHostPort("localhost", pprofPort), nil)
		if err != nil {
			logger.Errorf("Error starting pprof server: %s", err.Error())
		}
	}()

	logger.Info("Startup: Setting up the Metron agent")
	marshaller, err := initializeDopplerPool(config, batcher, logger)
	if err != nil {
		return fmt.Errorf("Could not initialize doppler connection pool: %s", err)
	}

	messageTagger := tagger.New(config.Deployment, config.Job, config.Index, marshaller)
	aggregator := messageaggregator.New(messageTagger, logger)
	eventWriter.SetWriter(aggregator)

	dropsondeUnmarshaller := eventunmarshaller.New(aggregator, batcher, logger)
	metronAddress := fmt.Sprintf("127.0.0.1:%d", config.IncomingUDPPort)

	// dropsondeReader, err := networkreader.NewNop(metronAddress, "dropsondeAgentListener", dropsondeUnmarshaller, logger)
	dropsondeReader, err := networkreader.NewPipe(metronAddress, "dropsondeAgentListener", reader, dropsondeUnmarshaller, logger)
	if err != nil {
		return fmt.Errorf("Failed to listen on %s: %s", metronAddress, err)
	}

	logger.Info("metron started")
	go dropsondeReader.Start()

	dumpChan := signalmanager.RegisterGoRoutineDumpSignalChannel()
	killChan := signalmanager.RegisterKillSignalChannel()

	stop := make(chan struct{})

	go func() {
		for {
			select {
			case <-dumpChan:
				signalmanager.DumpGoRoutine()
			case <-stop:
				logger.Info("Received stop signal")
				close(statsStopChan)
			case <-killChan:
				logger.Info("Shutting down")
				close(statsStopChan)
			}
		}
	}()

	return nil
}

func initializeDopplerPool(conf *config.Config, batcher *metricbatcher.MetricBatcher,
	logger *gosteno.Logger) (*eventmarshaller.EventMarshaller, error) {

	// NB: taken from tools/metronbenchmark/main.go
	adapter := announceToEtcd("udp://127.0.0.1:3457") // UDP

	backoffStrategy := retrystrategy.Exponential()
	err := backoff.Connect(adapter, backoffStrategy, logger, connectionRetries)
	if err != nil {
		return nil, err
	}

	udpCreator := clientpool.NewNopUDPClientCreator(logger)                            // WARN
	udpWrapper := dopplerforwarder.NewNopUDPWrapper([]byte(conf.SharedSecret), logger) // WARN
	udpPool := clientpool.NewDopplerPool(logger, udpCreator)
	udpForwarder := dopplerforwarder.New(udpWrapper, udpPool, logger)

	// clientPool := map[string]clientreader.ClientPool{"udp": udpPool}
	// writers[proto] = udpForwarder

	finder := dopplerservice.NewFinder(adapter, conf.LoggregatorDropsondePort,
		conf.Protocols.Strings(), conf.Zone, logger)
	finder.Start()

	marshaller := eventmarshaller.New(batcher, logger)
	marshaller.SetWriter(udpForwarder)

	udpPool.SetAddresses([]string{"0.0.0.0:1234"})

	return marshaller, nil
}

/////////////////////////////////////////////////////////////////////

func announceToEtcd(addresses ...string) storeadapter.StoreAdapter {
	etcdUrls := []string{"http://localhost:4001"}
	etcdMaxConcurrentRequests := 10
	storeAdapter := NewStoreAdapter(etcdUrls, etcdMaxConcurrentRequests)
	addressJSON, err := json.Marshal(map[string]interface{}{
		"endpoints": addresses,
	})
	if err != nil {
		panic(err)
	}
	node := storeadapter.StoreNode{
		Key:   NodeKey,
		Value: addressJSON,
	}
	err = storeAdapter.Create(node)
	if err != nil {
		panic(err)
	}
	time.Sleep(50 * time.Millisecond)
	return storeAdapter
}

func NewStoreAdapter(urls []string, concurrentRequests int) storeadapter.StoreAdapter {
	workPool, err := workpool.NewWorkPool(concurrentRequests)
	if err != nil {
		panic(err)
	}
	options := &etcdstoreadapter.ETCDOptions{
		ClusterUrls: urls,
	}
	etcdStoreAdapter, err := etcdstoreadapter.New(options, workPool)
	if err != nil {
		panic(err)
	}
	etcdStoreAdapter.Connect()
	return etcdStoreAdapter
}

/////////////////////////////////////////////////////////////////////

func initializeMetrics(config *config.Config, stopChan chan struct{},
	logger *gosteno.Logger) (*metricbatcher.MetricBatcher, *eventwriter.EventWriter) {

	eventWriter := eventwriter.New(origin)
	metricSender := metric_sender.NewMetricSender(eventWriter)
	metricBatcher := metricbatcher.New(metricSender,
		time.Duration(config.MetricBatchIntervalMilliseconds)*time.Millisecond)
	metrics.Initialize(metricSender, metricBatcher)

	stats := runtime_stats.NewRuntimeStats(eventWriter,
		time.Duration(config.RuntimeStatsIntervalMilliseconds)*time.Millisecond)
	go stats.Run(stopChan)
	return metricBatcher, eventWriter
}

func storeAdapterProvider(conf *config.Config) (storeadapter.StoreAdapter, error) {
	workPool, err := workpool.NewWorkPool(conf.EtcdMaxConcurrentRequests)
	if err != nil {
		return nil, err
	}

	options := &etcdstoreadapter.ETCDOptions{
		ClusterUrls: conf.EtcdUrls,
	}
	if conf.EtcdRequireTLS {
		options.IsSSL = true
		options.CertFile = conf.EtcdTLSClientConfig.CertFile
		options.KeyFile = conf.EtcdTLSClientConfig.KeyFile
		options.CAFile = conf.EtcdTLSClientConfig.CAFile
	}
	etcdAdapter, err := etcdstoreadapter.New(options, workPool)
	if err != nil {
		return nil, err
	}

	return etcdAdapter, nil
}

func randomString(n int) string { return string(randomChars(n)) }

func randomChars(n int) []byte {
	b := make([]byte, n)
	for i := 0; i < n; i++ {
		b[i] = byte(rand.Intn('~'-' ') + ' ')
	}
	return b
}

// TODO: Find which keys are important to metrics and make them configurable.
func valueMessage(n int) *events.Envelope {
	// WARN: n is not used
	val := &events.ValueMetric{
		Name:  proto.String(fmt.Sprintf("fake_metric_name_%d", rand.Intn(5))),
		Value: proto.Float64(rand.Float64()),
		Unit:  proto.String(fmt.Sprintf("fake_metric_unit_%d", rand.Intn(5))),
	}
	env := &events.Envelope{
		Origin:      proto.String("test-origin"),
		EventType:   events.Envelope_ValueMetric.Enum(),
		ValueMetric: val,
	}
	return env
}

// TODO: Find which keys are important to metrics and make them configurable.
func logMessage(n int) *events.Envelope {
	msg := &events.LogMessage{
		Message:     randomChars(n),
		MessageType: events.LogMessage_OUT.Enum(),
		Timestamp:   proto.Int64(time.Now().UnixNano()),
		AppId:       proto.String("AppId_" + randomString(5)),
	}
	env := &events.Envelope{
		Origin:     proto.String("test-origin"),
		EventType:  events.Envelope_LogMessage.Enum(),
		LogMessage: msg,
	}
	return env
}

func generateMessages(n int) ([][]byte, error) {
	msgs := make([][]byte, n)
	for i := 0; i < len(msgs); i++ {
		var e *events.Envelope
		if i&1 == 0 {
			e = valueMessage(rand.Intn(512))
		} else {
			e = logMessage(rand.Intn(512))
		}
		var err error
		msgs[i], err = proto.Marshal(e)
		if err != nil {
			return nil, err
		}
	}
	return msgs, nil
}

func main() {
	const Count = 10000000

	msgs, err := generateMessages(20)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("STARTING METRON")
	pr, pw := io.Pipe()
	if err := StartMetron(defaultConfig, pr); err != nil {
		log.Fatal(err)
	}
	fmt.Println("SENDING MESSAGES")

	t := time.Now()
	for i := 0; i < Count; i++ {
		n := i % len(msgs)
		if _, err := pw.Write(msgs[n]); err != nil {
			log.Fatalf("%d: %s", i, err)
		}
	}
	d := time.Since(t)

	fmt.Println(d, d/Count)

	if err := pw.Close(); err != nil {
		log.Fatalf("pw: %s", err)
	}
	if err := pr.Close(); err != nil {
		log.Fatalf("pr: %s", err)
	}
	fmt.Println("OK")
}
