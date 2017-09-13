package docker

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/DataDog/datadog-process-agent/util"
	log "github.com/cihub/seelog"
)

var (
	containerRe = regexp.MustCompile("[0-9a-f]{64}")
	// ErrMissingTarget is an error set when a cgroup target is missing.
	ErrMissingTarget = errors.New("Missing cgroup target")
)

// CgroupMemStat stores memory statistics about a cgroup.
type CgroupMemStat struct {
	ContainerID             string
	Cache                   uint64
	RSS                     uint64
	RSSHuge                 uint64
	MappedFile              uint64
	Pgpgin                  uint64
	Pgpgout                 uint64
	Pgfault                 uint64
	Pgmajfault              uint64
	InactiveAnon            uint64
	ActiveAnon              uint64
	InactiveFile            uint64
	ActiveFile              uint64
	Unevictable             uint64
	HierarchicalMemoryLimit uint64
	TotalCache              uint64
	TotalRSS                uint64
	TotalRSSHuge            uint64
	TotalMappedFile         uint64
	TotalPgpgIn             uint64
	TotalPgpgOut            uint64
	TotalPgFault            uint64
	TotalPgMajFault         uint64
	TotalInactiveAnon       uint64
	TotalActiveAnon         uint64
	TotalInactiveFile       uint64
	TotalActiveFile         uint64
	TotalUnevictable        uint64
	MemUsageInBytes         uint64
	MemFailCnt              uint64
}

// CgroupTimesStat stores CPU times for a cgroup.
type CgroupTimesStat struct {
	ContainerID string
	System      uint64
	User        uint64
}

// CgroupIOStat store I/O statistics about a cgroup.
type CgroupIOStat struct {
	ContainerID string
	ReadBytes   uint64
	WriteBytes  uint64
}

// ContainerCgroup is a structure that stores paths and mounts for a cgroup.
// It provides several methods for collecting stats about the cgroup using the
// paths and mounts metadata.
type ContainerCgroup struct {
	ContainerID string
	Pids        []int32
	Paths       map[string]string
	Mounts      map[string]string
}

// Mem returns the memory statistics for a Cgroup. If the cgroup file is not
// availble then we return an empty stats file.
func (c ContainerCgroup) Mem() (*CgroupMemStat, error) {
	ret := &CgroupMemStat{ContainerID: c.ContainerID}
	statfile := c.cgroupFilePath("memory", "memory.stat")

	f, err := os.Open(statfile)
	if os.IsNotExist(err) {
		log.Debugf("missing cgroup file: %s", statfile)
		return ret, nil
	} else if err != nil {
		return nil, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), " ")
		v, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch fields[0] {
		case "cache":
			ret.Cache = v
		case "rss":
			ret.RSS = v
		case "rssHuge":
			ret.RSSHuge = v
		case "mappedFile":
			ret.MappedFile = v
		case "pgpgin":
			ret.Pgpgin = v
		case "pgpgout":
			ret.Pgpgout = v
		case "pgfault":
			ret.Pgfault = v
		case "pgmajfault":
			ret.Pgmajfault = v
		case "inactiveAnon":
			ret.InactiveAnon = v
		case "activeAnon":
			ret.ActiveAnon = v
		case "inactiveFile":
			ret.InactiveFile = v
		case "activeFile":
			ret.ActiveFile = v
		case "unevictable":
			ret.Unevictable = v
		case "hierarchicalMemoryLimit":
			ret.HierarchicalMemoryLimit = v
		case "totalCache":
			ret.TotalCache = v
		case "totalRss":
			ret.TotalRSS = v
		case "totalRssHuge":
			ret.TotalRSSHuge = v
		case "totalMappedFile":
			ret.TotalMappedFile = v
		case "totalPgpgin":
			ret.TotalPgpgIn = v
		case "totalPgpgout":
			ret.TotalPgpgOut = v
		case "totalPgfault":
			ret.TotalPgFault = v
		case "totalPgmajfault":
			ret.TotalPgMajFault = v
		case "totalInactiveAnon":
			ret.TotalInactiveAnon = v
		case "totalActiveAnon":
			ret.TotalActiveAnon = v
		case "totalInactiveFile":
			ret.TotalInactiveFile = v
		case "totalActiveFile":
			ret.TotalActiveFile = v
		case "totalUnevictable":
			ret.TotalUnevictable = v
		}
	}
	if err := scanner.Err(); err != nil {
		return ret, fmt.Errorf("error reading %s: %s", statfile, err)
	}
	return ret, nil
}

// MemLimit returns the memory limit of the cgroup, if it exists. If the file does not
// exist or there is no limit then this will default to 0.
func (c ContainerCgroup) MemLimit() (uint64, error) {
	statfile := c.cgroupFilePath("memory", "memory.limit_in_bytes")
	lines, err := util.ReadLines(statfile)
	if os.IsNotExist(err) {
		log.Debugf("missing cgroup file: %s", statfile)
		return 0, nil
	} else if err != nil {
		return 0, err
	}
	if len(lines) != 1 {
		return 0, fmt.Errorf("wrong format file: %s", statfile)
	}
	v, err := strconv.ParseUint(lines[0], 10, 64)
	if err != nil {
		return 0, err
	}
	// limit_in_bytes is a special case here, it's possible that it shows a ridiculous number,
	// in which case it represents unlimited, so return 0 here
	if v > uint64(math.Pow(2, 60)) {
		v = 0
	}
	return v, nil
}

// CPU returns the CPU status for this cgroup instance
// If the cgroup file does not exist then we just log debug return nothing.
func (c ContainerCgroup) CPU() (*CgroupTimesStat, error) {
	ret := &CgroupTimesStat{ContainerID: c.ContainerID}
	statfile := c.cgroupFilePath("cpuacct", "cpuacct.stat")
	f, err := os.Open(statfile)
	if os.IsNotExist(err) {
		log.Debugf("missing cgroup file: %s", statfile)
		return ret, nil
	} else if err != nil {
		return nil, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), " ")
		if fields[0] == "user" {
			user, err := strconv.ParseUint(fields[1], 10, 64)
			if err == nil {
				ret.User = user
			}
		}
		if fields[0] == "system" {
			system, err := strconv.ParseUint(fields[1], 10, 64)
			if err == nil {
				ret.System = system
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return ret, fmt.Errorf("error reading %s: %s", statfile, err)
	}
	return ret, nil
}

// CPULimit would show CPU limit for this cgroup.
// It does so by checking the cpu period and cpu quota config
// if a user does this:
//
//	docker run --cpus='0.5' ubuntu:latest
//
// we should return 50% for that container
// If the limits files aren't available (on older version) then
// we'll return the default value of 100.
func (c ContainerCgroup) CPULimit() (float64, error) {
	periodFile := c.cgroupFilePath("cpu", "cpu.cfs_period_us")
	quotaFile := c.cgroupFilePath("cpu", "cpu.cfs_quota_us")
	plines, err := util.ReadLines(periodFile)
	if os.IsNotExist(err) {
		log.Debugf("missing cgroup file: %s", periodFile)
		return 100, nil
	} else if err != nil {
		return 0, err
	}
	qlines, err := util.ReadLines(quotaFile)
	if os.IsNotExist(err) {
		log.Debugf("missing cgroup file: %s", quotaFile)
		return 100, nil
	} else if err != nil {
		return 0, err
	}
	period, err := strconv.ParseFloat(plines[0], 64)
	if err != nil {
		return 0, err
	}
	quota, err := strconv.ParseFloat(qlines[0], 64)
	if err != nil {
		return 0, err
	}
	// default cpu limit is 100%
	limit := 100.0
	if (period > 0) && (quota > 0) {
		limit = (quota / period) * 100.0
	}
	return limit, nil
}

// IO returns the disk read and write bytes stats for this cgroup.
// Format:
//
// 8:0 Read 49225728
// 8:0 Write 9850880
// 8:0 Sync 0
// 8:0 Async 59076608
// 8:0 Total 59076608
// 252:0 Read 49094656
// 252:0 Write 9850880
// 252:0 Sync 0
// 252:0 Async 58945536
// 252:0 Total 58945536
//
func (c ContainerCgroup) IO() (*CgroupIOStat, error) {
	ret := &CgroupIOStat{ContainerID: c.ContainerID}
	statfile := c.cgroupFilePath("blkio", "blkio.throttle.io_service_bytes")
	f, err := os.Open(statfile)
	if os.IsNotExist(err) {
		log.Debugf("missing cgroup file: %s", statfile)
		return ret, nil
	} else if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), " ")
		if fields[1] == "Read" {
			read, err := strconv.ParseUint(fields[2], 10, 64)
			if err == nil {
				ret.ReadBytes = read
			}
		} else if fields[1] == "Write" {
			write, err := strconv.ParseUint(fields[2], 10, 64)
			if err == nil {
				ret.WriteBytes = write
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return ret, fmt.Errorf("error reading %s: %s", statfile, err)
	}
	return ret, nil
}

// ContainerStartTime gets the stat for cgroup directory and use the mtime for that dir to determine the start time for the container
// this should work because the cgroup dir for the container would be created only when it's started
func (c ContainerCgroup) ContainerStartTime() (int64, error) {
	cgroupDir := c.cgroupFilePath("cpuacct", "")
	if !util.PathExists(cgroupDir) {
		return 0, fmt.Errorf("could not get cgroup dir, directory doesn't exist")
	}
	stat, err := os.Stat(cgroupDir)
	if err != nil {
		return 0, fmt.Errorf("could not get stat of the cgroup dir: %s", err)
	}
	return stat.ModTime().Unix(), nil
}

// cgroupFilePath constructs file path to get targetted stats file.
func (c ContainerCgroup) cgroupFilePath(target, file string) string {
	mount, ok := c.Mounts[target]
	if !ok {
		log.Errorf("missing target %s from mounts", target)
		return ""
	}
	targetPath, ok := c.Paths[target]
	if !ok {
		log.Errorf("missing target %s from paths", target)
		return ""
	}

	return filepath.Join(mount, targetPath, file)
}

// function to get the mount point of all cgroup. by default it should be under /sys/fs/cgroup but
// it could be mounted anywhere else if manually defined. Example cgroup entries in /proc/mounts would be
//	 cgroup /sys/fs/cgroup/cpuset cgroup rw,relatime,cpuset 0 0
//	 cgroup /sys/fs/cgroup/cpu cgroup rw,relatime,cpu 0 0
//	 cgroup /sys/fs/cgroup/cpuacct cgroup rw,relatime,cpuacct 0 0
//	 cgroup /sys/fs/cgroup/memory cgroup rw,relatime,memory 0 0
//	 cgroup /sys/fs/cgroup/devices cgroup rw,relatime,devices 0 0
//	 cgroup /sys/fs/cgroup/freezer cgroup rw,relatime,freezer 0 0
//	 cgroup /sys/fs/cgroup/blkio cgroup rw,relatime,blkio 0 0
//	 cgroup /sys/fs/cgroup/perf_event cgroup rw,relatime,perf_event 0 0
//	 cgroup /sys/fs/cgroup/hugetlb cgroup rw,relatime,hugetlb 0 0
//
// Returns a map for every target (cpuset, cpu, cpuacct) => path
func cgroupMountPoints() (map[string]string, error) {
	mountsFile := "/proc/mounts"
	if !util.PathExists(mountsFile) {
		return nil, fmt.Errorf("/proc/mounts does not exist")
	}
	f, err := os.Open(mountsFile)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return parseCgroupMountPoints(f), nil
}

func parseCgroupMountPoints(r io.Reader) map[string]string {
	mountPoints := make(map[string]string)
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		mount := scanner.Text()
		if strings.HasPrefix(mount, "cgroup ") {
			tokens := strings.Split(mount, " ")
			cgroupPath := tokens[1]

			// Re-point /sys cgroups to /proc/sys
			if strings.HasPrefix(cgroupPath, "/sys") {
				cgroupPath = util.HostSys(strings.TrimPrefix(cgroupPath, "/sys"))
			}

			// Target can be comma-separate values like cpu,cpuacct
			tsp := strings.Split(path.Base(cgroupPath), ",")
			for _, target := range tsp {
				mountPoints[target] = cgroupPath
			}
		}
	}
	return mountPoints
}

// CgroupsForPids returns ContainerCgroup for every container that's in a Cgroup.
// We return as a map[containerID]Cgroup for easy look-up.
func CgroupsForPids(pids []int32) (map[string]*ContainerCgroup, error) {
	mountPoints, err := cgroupMountPoints()
	if err != nil {
		return nil, err
	}

	cgs := make(map[string]*ContainerCgroup)
	for _, pid := range pids {
		cgPath := util.HostProc(strconv.Itoa(int(pid)), "cgroup")
		containerID, paths, err := readCgroupPaths(cgPath)
		if containerID == "" {
			continue
		}
		if err != nil {
			log.Debugf("error reading cgroup paths %s: %s", cgPath, err)
			continue
		}
		if cg, ok := cgs[containerID]; ok {
			// Assumes that the paths will always be the same for a container id.
			cg.Pids = append(cg.Pids, pid)
		} else {
			cgs[containerID] = &ContainerCgroup{
				ContainerID: containerID,
				Pids:        []int32{pid},
				Paths:       paths,
				Mounts:      mountPoints}
		}
	}
	return cgs, nil
}

// readCgroupPaths reads the cgroups from a /sys/$pid/cgroup path.
func readCgroupPaths(pidCgroupPath string) (string, map[string]string, error) {
	f, err := os.Open(pidCgroupPath)
	if os.IsNotExist(err) {
		log.Debugf("cgroup path '%s' could not be read: %s", pidCgroupPath, err)
		return "", nil, nil
	} else if err != nil {
		log.Debugf("cgroup path '%s' could not be read: %s", pidCgroupPath, err)
		return "", nil, err
	}
	defer f.Close()
	return parseCgroupPaths(f)
}

// parseCgroupPaths parses out the cgroup paths from a /proc/$pid/cgroup file.
// The file format will be something like:
//
// 11:net_cls:/kubepods/besteffort/pod2baa3444-4d37-11e7-bd2f-080027d2bf10/47fc31db38b4fa0f4db44b99d0cad10e3cd4d5f142135a7721c1c95c1aadfb2e
// 10:freezer:/kubepods/besteffort/pod2baa3444-4d37-11e7-bd2f-080027d2bf10/47fc31db38b4fa0f4db44b99d0cad10e3cd4d5f142135a7721c1c95c1aadfb2e
// 9:cpu,cpuacct:/kubepods/besteffort/pod2baa3444-4d37-11e7-bd2f-080027d2bf10/47fc31db38b4fa0f4db44b99d0cad10e3cd4d5f142135a7721c1c95c1aadfb2e
// 8:memory:/kubepods/besteffort/pod2baa3444-4d37-11e7-bd2f-080027d2bf10/47fc31db38b4fa0f4db44b99d0cad10e3cd4d5f142135a7721c1c95c1aadfb2e
// 7:blkio:/kubepods/besteffort/pod2baa3444-4d37-11e7-bd2f-080027d2bf10/47fc31db38b4fa0f4db44b99d0cad10e3cd4d5f142135a7721c1c95c1aadfb2e
//
// Returns the common containerID and a mapping of target => path
// If the first line doesn't have a valid container ID we will return an empty string
func parseCgroupPaths(r io.Reader) (string, map[string]string, error) {
	var ok bool
	var containerID string
	paths := make(map[string]string)
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		l := scanner.Text()
		if containerID == "" {
			// Check if this process running inside a container.
			containerID, ok = containerIDFromCgroup(l)
			if !ok {
				log.Debugf("could not parse container id from path '%s'", l)
				return "", nil, nil
			}
		}

		sp := strings.SplitN(l, ":", 3)
		if len(sp) < 3 {
			continue
		}
		// Target can be comma-separate values like cpu,cpuacct
		tsp := strings.Split(sp[1], ",")
		for _, target := range tsp {
			paths[target] = sp[2]
		}
	}
	if err := scanner.Err(); err != nil {
		return "", nil, err
	}

	// In Ubuntu Xenial, we've encountered containers with no `cpu`
	_, cpuok := paths["cpu"]
	cpuacct, cpuacctok := paths["cpuacct"]
	if !cpuok && cpuacctok {
		paths["cpu"] = cpuacct
	}

	return containerID, paths, nil
}

func containerIDFromCgroup(cgroup string) (string, bool) {
	sp := strings.SplitN(cgroup, ":", 3)
	if len(sp) < 3 {
		return "", false
	}
	match := containerRe.Find([]byte(sp[2]))
	if match == nil {
		return "", false
	}
	return string(match), true
}
