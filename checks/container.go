package checks

import (
	"runtime"
	"time"

	"github.com/DataDog/gopsutil/cpu"
	log "github.com/cihub/seelog"

	"github.com/DataDog/datadog-agent/pkg/util/docker"
	"github.com/DataDog/datadog-process-agent/config"
	"github.com/DataDog/datadog-process-agent/model"
	"github.com/DataDog/datadog-process-agent/statsd"
	"github.com/DataDog/datadog-process-agent/util/ecs"
	"github.com/DataDog/datadog-process-agent/util/kubernetes"
)

// Container is a singleton ContainerCheck.
var Container = &ContainerCheck{}

// ContainerCheck is a check that returns container metadata and stats.
type ContainerCheck struct {
	sysInfo        *model.SystemInfo
	lastCPUTime    cpu.TimesStat
	lastContainers []*docker.Container
	lastRun        time.Time
}

// Init initializes a ContainerCheck instance.
func (c *ContainerCheck) Init(cfg *config.AgentConfig, info *model.SystemInfo) {
	c.sysInfo = info
}

// Name returns the name of the ProcessCheck.
func (c *ContainerCheck) Name() string { return "container" }

// Endpoint returns the endpoint where this check is submitted.
func (c *ContainerCheck) Endpoint() string { return "/api/v1/container" }

// RealTime indicates if this check only runs in real-time mode.
func (c *ContainerCheck) RealTime() bool { return false }

// Run runs the ContainerCheck to collect a list of running containers and the
// stats for each container.
func (c *ContainerCheck) Run(cfg *config.AgentConfig, groupID int32) ([]model.MessageBody, error) {
	start := time.Now()
	cpuTimes, err := cpu.Times(false)
	if err != nil {
		return nil, err
	}
	containers, err := docker.AllContainers(&docker.ContainerListConfig{})
	if err != nil && err != docker.ErrDockerNotAvailable {
		return nil, err
	}

	// End check early if this is our first run.
	if c.lastContainers == nil {
		c.lastContainers = containers
		c.lastCPUTime = cpuTimes[0]
		c.lastRun = time.Now()
		return nil, nil
	}

	// Fetch orchestrator metadata once per check.
	ecsMeta := ecs.GetMetadata()
	kubeMeta := kubernetes.GetMetadata()

	groupSize := len(containers) / cfg.ProcLimit
	if len(containers) != cfg.ProcLimit {
		groupSize++
	}
	chunked := fmtContainers(containers, c.lastContainers,
		cpuTimes[0], c.lastCPUTime, c.lastRun, groupSize)
	messages := make([]model.MessageBody, 0, groupSize)
	totalContainers := float64(0)
	for i := 0; i < groupSize; i++ {
		totalContainers += float64(len(chunked[i]))
		messages = append(messages, &model.CollectorContainer{
			HostName:   cfg.HostName,
			Info:       c.sysInfo,
			Containers: chunked[i],
			GroupId:    groupID,
			GroupSize:  int32(groupSize),
			Kubernetes: kubeMeta,
			Ecs:        ecsMeta,
		})
	}

	c.lastCPUTime = cpuTimes[0]
	c.lastContainers = containers
	c.lastRun = time.Now()

	statsd.Client.Gauge("datadog.process.containers.host_count", totalContainers, []string{}, 1)
	log.Debugf("collected containers in %s", time.Now().Sub(start))
	return messages, nil
}

// fmtContainers formats and chunks the containers into a slice of chunks using a specific
// number of chunks. len(result) MUST EQUAL chunks.
func fmtContainers(
	containers, lastContainers []*docker.Container,
	syst2, syst1 cpu.TimesStat,
	lastRun time.Time,
	chunks int,
) [][]*model.Container {
	lastByID := make(map[string]*docker.Container, len(containers))
	for _, c := range lastContainers {
		lastByID[c.ID] = c
	}

	perChunk := (len(containers) / chunks) + 1
	chunked := make([][]*model.Container, chunks)
	chunk := make([]*model.Container, 0, perChunk)
	i := 0
	for _, ctr := range containers {
		lastCtr, ok := lastByID[ctr.ID]
		if !ok {
			// Set to an empty container so rate calculations work and use defaults.
			lastCtr = docker.NullContainer
		}

		ifStats := ctr.Network.SumInterfaces()
		lastIfStats := lastCtr.Network.SumInterfaces()
		cpus := runtime.NumCPU()
		chunk = append(chunk, &model.Container{
			Type:        ctr.Type,
			Name:        ctr.Name,
			Id:          ctr.ID,
			Image:       ctr.Image,
			CpuLimit:    float32(ctr.CPULimit),
			UserPct:     calculateCtrPct(ctr.CPU.User, lastCtr.CPU.User, cpus, lastRun),
			SystemPct:   calculateCtrPct(ctr.CPU.System, lastCtr.CPU.System, cpus, lastRun),
			TotalPct:    calculateCtrPct(ctr.CPU.User+ctr.CPU.System, lastCtr.CPU.User+lastCtr.CPU.System, cpus, lastRun),
			MemoryLimit: ctr.MemLimit,
			MemRss:      ctr.Memory.RSS,
			MemCache:    ctr.Memory.Cache,
			Created:     ctr.Created,
			State:       model.ContainerState(model.ContainerState_value[ctr.State]),
			Health:      model.ContainerHealth(model.ContainerHealth_value[ctr.Health]),
			Rbps:        calculateRate(ctr.IO.ReadBytes, lastCtr.IO.ReadBytes, lastRun),
			Wbps:        calculateRate(ctr.IO.WriteBytes, lastCtr.IO.WriteBytes, lastRun),
			NetRcvdPs:   calculateRate(ifStats.PacketsRcvd, lastIfStats.PacketsRcvd, lastRun),
			NetSentPs:   calculateRate(ifStats.PacketsSent, lastIfStats.PacketsSent, lastRun),
			NetRcvdBps:  calculateRate(ifStats.BytesRcvd, lastIfStats.BytesRcvd, lastRun),
			NetSentBps:  calculateRate(ifStats.BytesSent, lastIfStats.BytesSent, lastRun),
			Started:     ctr.StartedAt,
		})

		if len(chunk) == perChunk {
			chunked[i] = chunk
			chunk = make([]*model.Container, 0, perChunk)
			i++
		}
	}
	if len(chunk) > 0 {
		chunked[i] = chunk
	}
	return chunked
}

func calculateCtrPct(cur, prev uint64, numCPU int, before time.Time) float32 {
	now := time.Now()
	diff := now.Unix() - before.Unix()
	if before.IsZero() || diff <= 0 {
		return 0
	}
	return float32(cur-prev) / float32(diff)
}
