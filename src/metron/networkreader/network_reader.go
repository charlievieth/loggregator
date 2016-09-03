package networkreader

import (
	"io"
	"math/rand"
	"net"
	"sync/atomic"
	"time"

	"metron/writers"

	"github.com/cloudfoundry/dropsonde/logging"
	"github.com/cloudfoundry/dropsonde/metrics"
	"github.com/cloudfoundry/gosteno"
	"github.com/cloudfoundry/sonde-go/events"
	"github.com/gogo/protobuf/proto"
)

type NetworkReader struct {
	connection net.PacketConn
	writer     writers.ByteArrayWriter

	contextName string

	logger *gosteno.Logger
}

func New(address string, name string, writer writers.ByteArrayWriter, logger *gosteno.Logger) (*NetworkReader, error) {
	connection, err := net.ListenPacket("udp4", address)
	if err != nil {
		return nil, err
	}
	logger.Infof("Listening on %s", address)

	return &NetworkReader{
		connection:  connection,
		contextName: name,
		writer:      writer,
		logger:      logger,
	}, nil
}

func (nr *NetworkReader) Start() {
	receivedMessageCountName := nr.contextName + ".receivedMessageCount"
	receivedByteCountName := nr.contextName + ".receivedByteCount"

	readBuffer := make([]byte, 65535) //buffer with size = max theoretical UDP size
	for {
		readCount, senderAddr, err := nr.connection.ReadFrom(readBuffer)
		if err != nil {
			nr.logger.Errorf("Error while reading. %s", err)
			return
		}
		logging.Debugf(nr.logger, "NetworkReader: Read %d bytes from address %s", readCount, senderAddr)
		readData := make([]byte, readCount) //pass on buffer in size only of read data
		copy(readData, readBuffer[:readCount])

		metrics.BatchIncrementCounter(receivedMessageCountName)
		metrics.BatchAddCounter(receivedByteCountName, uint64(readCount))
		nr.writer.Write(readData)
	}
}

func (nr *NetworkReader) Stop() {
	nr.connection.Close()
}

////////////////////////////////////////

type PipeNetworkReader struct {
	reader      *io.PipeReader
	writer      writers.ByteArrayWriter
	logger      *gosteno.Logger
	contextName string
	state       int32
}

func NewPipe(address, name string, reader *io.PipeReader, writer writers.ByteArrayWriter,
	logger *gosteno.Logger) (*PipeNetworkReader, error) {

	logger.Infof("Listening on %s", address)
	r := &PipeNetworkReader{
		reader:      reader,
		writer:      writer,
		logger:      logger,
		contextName: name,
	}
	return r, nil
}

func (r *PipeNetworkReader) Start() {
	if !atomic.CompareAndSwapInt32(&r.state, 0, 1) {
		panic("NopNetworkReader: already started")
	}

	receivedMessageCountName := r.contextName + ".receivedMessageCount"
	receivedByteCountName := r.contextName + ".receivedByteCount"

	readBuffer := make([]byte, 65535) //buffer with size = max theoretical UDP size

	for atomic.LoadInt32(&r.state) == 1 {
		n, err := r.reader.Read(readBuffer)
		if err != nil {
			r.logger.Errorf("Error while reading. %s", err)
			return
		}
		logging.Debugf(r.logger, "NetworkReader: Read %d bytes from address %s", n, "Address")
		readData := make([]byte, n)
		copy(readData, readBuffer[:n])

		metrics.BatchIncrementCounter(receivedMessageCountName)
		metrics.BatchAddCounter(receivedByteCountName, uint64(n))
		r.writer.Write(readData)
	}
}

func (r *PipeNetworkReader) Stop() { atomic.StoreInt32(&r.state, 0) }

////////////////////////////////////////

type NopNetworkReader struct {
	writer writers.ByteArrayWriter

	contextName string

	logger *gosteno.Logger
	msgs   [][]byte
	state  int32
}

func NewNop(address string, name string, writer writers.ByteArrayWriter, logger *gosteno.Logger) (*NopNetworkReader, error) {
	const RandMsgs = 50
	logger.Infof("Listening on %s", address)

	msgs := make([][]byte, RandMsgs)
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

	return &NopNetworkReader{
		contextName: name,
		writer:      writer,
		logger:      logger,
		msgs:        msgs,
		state:       0,
	}, nil
}

func (r *NopNetworkReader) Start() {
	if !atomic.CompareAndSwapInt32(&r.state, 0, 1) {
		panic("NopNetworkReader: already started")
	}

	receivedMessageCountName := r.contextName + ".receivedMessageCount"
	receivedByteCountName := r.contextName + ".receivedByteCount"

	for atomic.LoadInt32(&r.state) == 1 {
		b := r.msgs[rand.Intn(len(r.msgs)-1)]
		logging.Debugf(r.logger, "NetworkReader: Read %d bytes from address %s", len(b), "Address")

		metrics.BatchIncrementCounter(receivedMessageCountName)
		metrics.BatchAddCounter(receivedByteCountName, uint64(len(b)))
		r.writer.Write(b)
	}
}

func (r *NopNetworkReader) Stop() { atomic.StoreInt32(&r.state, 0) }

func randomString(n int) string { return string(randomChars(n)) }

func randomChars(n int) []byte {
	b := make([]byte, n)
	for i := 0; i < n; i++ {
		b[i] = byte(rand.Intn('~'-' ') + ' ')
	}
	return b
}

func valueMessage(n int) *events.Envelope {
	val := &events.ValueMetric{
		Name:  proto.String("metric_" + randomString(5)),
		Value: proto.Float64(rand.Float64()),
		Unit:  proto.String("unit_" + "metric_" + randomString(5)),
	}
	env := &events.Envelope{
		Origin:      proto.String("origin_" + randomString(5)),
		EventType:   events.Envelope_LogMessage.Enum(),
		ValueMetric: val,
	}
	return env
}

func logMessage(n int) *events.Envelope {
	msg := &events.LogMessage{
		Message:     randomChars(n),
		MessageType: events.LogMessage_OUT.Enum(),
		Timestamp:   proto.Int64(time.Now().UnixNano()),
		AppId:       proto.String("AppId_" + randomString(5)),
	}
	env := &events.Envelope{
		Origin:     proto.String("origin_" + randomString(5)),
		EventType:  events.Envelope_LogMessage.Enum(),
		LogMessage: msg,
	}
	return env
}
