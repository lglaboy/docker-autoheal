package main

import (
	"flag"
	"fmt"
	"github.com/docker/docker/api/types/filters"
	"gopkg.in/yaml.v3"
	"log"
	"os"
	"sync"
	"time"

	"context"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

var (
	restartRecords *RestartRecords
	c              Config
	sc             Server
)

const Format = "2006-01-02 15:04:05"

type Server struct {
	DockerAPIVersion string `yaml:"docker_api_version"`
	Interval         int    `yaml:"interval"`
	ResetBackoff     int    `yaml:"reset_backoff"`
	BaseBackoff      int    `yaml:"base_backoff"`
	MaximumBackoff   int    `yaml:"maximum_backoff"`
}

type Config struct {
	Server `yaml:"server"`
}

type RestartRecord struct {
	ContainerID  string
	RestartCount int
	RestartTime  time.Time
	Restarting   bool
	WaitTime     time.Time
}

type RestartRecords struct {
	records []RestartRecord
	mu      sync.Mutex
}

func (r *RestartRecord) setRestarting(restart bool) {
	r.Restarting = restart
}

func (r *RestartRecords) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.records)
}

func (r *RestartRecords) Check(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, restart := range r.records {
		if restart.ContainerID == id {
			return true
		}
	}
	return false
}

func (r *RestartRecords) Add(id string, rc int, rt time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, RestartRecord{ContainerID: id, RestartCount: rc, RestartTime: rt})
}

func (r *RestartRecords) Get(id string) *RestartRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.records {
		if (r.records)[i].ContainerID == id {
			return &(r.records)[i]
		}
	}
	return nil
}

func logInfo(format string, v ...interface{}) {
	log.Printf("[INFO] "+format, v...)
}

func logError(format string, v ...interface{}) {
	log.Printf("[ERROR] "+format, v...)
}

func validateConfig() error {
	if sc.Interval <= 0 || sc.BaseBackoff <= 0 || sc.MaximumBackoff <= 0 {
		return fmt.Errorf("配置值错误: Interval, BaseBackoff, MaximumBackoff 必须大于 0")
	}
	return nil
}

func loadConfig() {
	var configFile string
	flag.StringVar(&configFile, "c", "./config.yml", "config file")
	flag.Parse()

	yamlFile, err := os.ReadFile(configFile)
	if err != nil {
		log.Fatal(err)
	}

	err = yaml.Unmarshal(yamlFile, &c)
	if err != nil {
		log.Fatal(err)
	}
	sc = c.Server

	// 验证配置文件
	if err := validateConfig(); err != nil {
		log.Fatal(err)
	}

	// 输出配置信息
	logInfo("采用的配置文件: %s", configFile)
	logInfo("Docker API Version: %s", sc.DockerAPIVersion)
	logInfo("检查异常容器间隔: %d s", sc.Interval)
	logInfo("采用指数级回退延迟机制重启容器，初始: %d s, 上限: %d s, 重置时间: %d s",
		sc.BaseBackoff, sc.MaximumBackoff, sc.ResetBackoff)
}

// 重启容器
func restartContainer(c *client.Client, id string, r *RestartRecord) {
	r.setRestarting(true)
	logInfo("开始重启容器: %v", id)
	if err := c.ContainerRestart(context.Background(), id, container.StopOptions{}); err != nil {
		logError("容器: %v 重启失败, %v", id, err)
	} else {
		logInfo("容器: %v 重启成功", id)
	}
	r.setRestarting(false)
}

func autoCheck() {
	apiClient, err := client.NewClientWithOpts(client.FromEnv, client.WithVersion(sc.DockerAPIVersion))
	if err != nil {
		log.Fatalf("创建 Docker 客户端失败: %v\n", err)
	}
	defer apiClient.Close()

	// 获取unhealthy容器列表
	filters := filters.NewArgs()
	filters.Add("health", "unhealthy")
	filters.Add("status", "running")
	//if someCondition {
	//	filters.Add("label", "someLabel=true")
	//}

	containers, err := apiClient.ContainerList(
		context.Background(),
		container.ListOptions{
			All:     true,
			Filters: filters,
		},
	)
	if err != nil {
		logError("获取 Docker unhealthy状态容器失败: %v", err)
	}

	for _, ctr := range containers {
		logInfo("%s %s (status: %s) 发现unhealthy容器", ctr.Names, ctr.ID, ctr.Status)

		if !restartRecords.Check(ctr.ID) {
			restartRecords.Add(ctr.ID, 0, time.Now())
			r := restartRecords.Get(ctr.ID)

			restartContainer(apiClient, ctr.ID, r)
		} else {
			r := restartRecords.Get(ctr.ID)
			if r == nil {
				logError("从重启记录中获取信息失败")
				continue
			}

			if r.Restarting {
				logInfo("容器: %v 重启中", ctr.ID)
				continue
			}

			expireT := r.RestartTime.Add(time.Second * time.Duration(sc.ResetBackoff))
			// 判断记录是否过期
			if time.Now().Before(expireT) {
				// 时间内重复unhealthy
				sleep := sc.BaseBackoff * (1 << r.RestartCount)
				if sleep > sc.MaximumBackoff {
					sleep = sc.MaximumBackoff
				}

				// 首次指数级回退
				if r.WaitTime.IsZero() {
					r.WaitTime = time.Now().Add(time.Duration(sleep) * time.Second)

					logInfo("采用指数级回退延迟机制重启容器: %v 中，等待: %v s, 重启时间: %v", ctr.ID, sleep, r.WaitTime.Format(Format))
				} else {
					if time.Now().After(r.WaitTime) {
						restartContainer(apiClient, ctr.ID, r)
						// 指数增加
						r.RestartCount++
						r.RestartTime = time.Now()
						// 本次结束，重置为0
						r.WaitTime = time.Time{}
					} else {
						logInfo("容器: %v 指数回退等待中, %v s, 截至时间 %v ", ctr.ID, sleep, r.WaitTime.Format(Format))
					}
					continue
				}

			} else {
				// 记录重置
				logInfo("容器健康运行超过 %v s, 重置记录", sc.ResetBackoff)
				r.RestartCount = 0
				r.RestartTime = time.Now()

				restartContainer(apiClient, ctr.ID, r)
			}
		}
	}
}

func autoHeal() {
	// 按照指定时间间隔检查异常容器
	interval := time.Duration(sc.Interval) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// 保持主函数一直运行
	for {
		select {
		case <-ticker.C: // 每次 ticker 间隔到达时触发
			go autoCheck() // 执行检查函数
		}
	}
}

func init() {
	restartRecords = new(RestartRecords)
}

func main() {
	// 加载配置
	loadConfig()

	// 自动检测unhealthy容器
	autoHeal()
}
