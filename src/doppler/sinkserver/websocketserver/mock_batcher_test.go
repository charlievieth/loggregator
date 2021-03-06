// This file was generated by github.com/nelsam/hel.  Do not
// edit this code by hand unless you *really* know what you're
// doing.  Expect any changes made manually to be overwritten
// the next time hel regenerates this file.

package websocketserver_test

import "github.com/cloudfoundry/dropsonde/metricbatcher"

type mockBatcher struct {
	BatchIncrementCounterCalled chan bool
	BatchIncrementCounterInput  struct {
		Name chan string
	}
	BatchCounterCalled chan bool
	BatchCounterInput  struct {
		Name chan string
	}
	BatchCounterOutput struct {
		Ret0 chan metricbatcher.BatchCounterChainer
	}
}

func newMockBatcher() *mockBatcher {
	m := &mockBatcher{}
	m.BatchIncrementCounterCalled = make(chan bool, 100)
	m.BatchIncrementCounterInput.Name = make(chan string, 100)
	m.BatchCounterCalled = make(chan bool, 100)
	m.BatchCounterInput.Name = make(chan string, 100)
	m.BatchCounterOutput.Ret0 = make(chan metricbatcher.BatchCounterChainer, 100)
	return m
}
func (m *mockBatcher) BatchIncrementCounter(name string) {
	m.BatchIncrementCounterCalled <- true
	m.BatchIncrementCounterInput.Name <- name
}
func (m *mockBatcher) BatchCounter(name string) metricbatcher.BatchCounterChainer {
	m.BatchCounterCalled <- true
	m.BatchCounterInput.Name <- name
	return <-m.BatchCounterOutput.Ret0
}
