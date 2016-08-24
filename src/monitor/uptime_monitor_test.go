package monitor_test

import (
	"monitor"
	"sync"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/cloudfoundry/sonde-go/events"
	"github.com/gogo/protobuf/proto"
)

const (
	interval = 100 * time.Millisecond
)

var _ = Describe("Uptime", func() {
	var (
		uptime *monitor.Uptime
		wg     sync.WaitGroup
	)

	BeforeEach(func() {
		fakeEventEmitter.Reset()
		uptime = monitor.NewUptime(interval)
		wg.Add(1)
		go func() {
			defer wg.Done()
			uptime.Start()
		}()
	})

	Context("stops automatically", func() {

		AfterEach(func() {
			uptime.Stop()
			wg.Wait()
		})

		It("returns a value metric containing uptime after specified time", func() {
			Eventually(fakeEventEmitter.GetMessages).Should(HaveLen(1))
			Expect(fakeEventEmitter.GetMessages()[0].Event.(*events.ValueMetric)).To(Equal(&events.ValueMetric{
				Name:  proto.String("Uptime"),
				Value: proto.Float64(0),
				Unit:  proto.String("seconds"),
			}))
		})

		It("reports increasing uptime value", func() {
			Eventually(fakeEventEmitter.GetMessages).Should(HaveLen(1))
			Expect(fakeEventEmitter.GetMessages()[0].Event.(*events.ValueMetric)).To(Equal(&events.ValueMetric{
				Name:  proto.String("Uptime"),
				Value: proto.Float64(0),
				Unit:  proto.String("seconds"),
			}))

			Eventually(getLatestUptime).Should(Equal(1.0))
		})
	})

	It("stops the monitor and respective ticker", func() {
		Eventually(func() int { return len(fakeEventEmitter.GetMessages()) }).Should(BeNumerically(">=", 1))

		uptime.Stop()

		current := getLatestUptime()
		Consistently(getLatestUptime, 2).Should(Equal(current))
	})
})

func getLatestUptime() float64 {
	lastMsgIndex := len(fakeEventEmitter.GetMessages()) - 1
	return *fakeEventEmitter.GetMessages()[lastMsgIndex].Event.(*events.ValueMetric).Value
}
