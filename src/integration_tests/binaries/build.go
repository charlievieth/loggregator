package binaries

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"github.com/onsi/gomega/gexec"
)

type BuildPaths struct {
	Metron            string `json:"metron"`
	Doppler           string `json:"doppler"`
	TrafficController string `json:"traffic_controller"`
	Etcd              string `json:"etcd"`
}

func (bp BuildPaths) Marshal() ([]byte, error) {
	return json.Marshal(bp)
}

func (bp *BuildPaths) Unmarshal(text []byte) error {
	return json.Unmarshal(text, bp)
}

func (bp BuildPaths) SetEnv() {
	os.Setenv("METRON_BUILD_PATH", bp.Metron)
	os.Setenv("DOPPLER_BUILD_PATH", bp.Doppler)
	os.Setenv("TRAFFIC_CONTROLLER_BUILD_PATH", bp.TrafficController)
	os.Setenv("ETCD_BUILD_PATH", bp.Etcd)
}

func Build() (BuildPaths, chan error) {
	var bp BuildPaths
	errors := make(chan error)
	defer close(errors)

	if os.Getenv("SKIP_BUILD") != "" {
		fmt.Println("Skipping building of binaries")
		bp.Metron = os.Getenv("METRON_BUILD_PATH")
		bp.Doppler = os.Getenv("DOPPLER_BUILD_PATH")
		bp.TrafficController = os.Getenv("TRAFFIC_CONTROLLER_BUILD_PATH")
		bp.Etcd = os.Getenv("ETCD_BUILD_PATH")
		return bp, errors
	}

	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)
	wg.Add(4)

	go func() {
		defer wg.Done()
		metronPath, err := gexec.Build("metron", "-race")
		if err != nil {
			errors <- err
			return
		}
		mu.Lock()
		defer mu.Unlock()
		bp.Metron = metronPath
	}()

	go func() {
		defer wg.Done()
		dopplerPath, err := gexec.Build("doppler", "-race")
		if err != nil {
			errors <- err
			return
		}
		mu.Lock()
		defer mu.Unlock()
		bp.Doppler = dopplerPath
	}()

	go func() {
		defer wg.Done()
		tcPath, err := gexec.Build("trafficcontroller", "-race")
		if err != nil {
			errors <- err
			return
		}
		mu.Lock()
		defer mu.Unlock()
		bp.TrafficController = tcPath
	}()

	go func() {
		defer wg.Done()
		etcdPath, err := gexec.Build("github.com/coreos/etcd", "-race")
		if err != nil {
			errors <- err
			return
		}
		mu.Lock()
		defer mu.Unlock()
		bp.Etcd = etcdPath
	}()

	wg.Wait()
	return bp, errors
}

func Cleanup() {
	gexec.CleanupBuildArtifacts()
}
