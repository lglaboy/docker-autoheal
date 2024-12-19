package main

import (
	"flag"
	"fmt"
	"github.com/docker/docker/api/types/filters"
	"gopkg.in/yaml.v3"
	"log"
	"math"
	"net/http"
	"os"
	"time"

	"context"
	"github.com/gin-gonic/gin"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

type RestartRecord struct {
	ContainerID  string
	RestartCount int
	RestartTime  time.Time
	Restarting   bool
}

type RestartRecords []RestartRecord

func (r *RestartRecords) Len() int {
	return len(*r)
}

func (r *RestartRecords) Check(id string) bool {
	for _, restart := range *r {
		if restart.ContainerID == id {
			return true
		}
	}
	return false
}

func (r *RestartRecords) Add(id string, rc int, rt time.Time) {
	*r = append(*r, RestartRecord{ContainerID: id, RestartCount: rc, RestartTime: rt})
}

func (r *RestartRecords) Get(id string) *RestartRecord {
	for _, restart := range *r {
		if restart.ContainerID == id {
			return &restart
		}
	}
	return nil
}

var restartRecords *RestartRecords

type Server struct {
	Port             string  `yaml:"port"`
	DockerAPIVersion string  `yaml:"docker_api_version"`
	GinMode          string  `yaml:"gin_mode"`
	Interval         int     `yaml:"interval"`
	ResetBackoff     float64 `yaml:"reset_backoff"`
	BaseBackoff      float64 `yaml:"base_backoff"`
	MaximumBackoff   float64 `yaml:"maximum_backoff"`
}

type Config struct {
	Server `yaml:"server"`
}

var c Config
var sc Server

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

	gin.SetMode(sc.GinMode)

	// 输出配置信息
	log.Printf("检查异常容器间隔: %d s\n", sc.Interval)
	log.Printf("检查异常容器间隔: %d s,采用指数退避算法重启\n", sc.Interval)
	log.Printf("Docker API Version: %s\n", sc.DockerAPIVersion)
	log.Printf("监听端口: %s, GIN模式: %s\n", ":"+sc.Port, sc.GinMode)
}

func autoCheck() {
	apiClient, err := client.NewClientWithOpts(client.FromEnv, client.WithVersion(sc.DockerAPIVersion))
	if err != nil {
		panic(err)
	}
	defer apiClient.Close()

	containers, err := apiClient.ContainerList(
		context.Background(),
		container.ListOptions{
			All: true,
			Filters: filters.NewArgs(
				filters.Arg("health", "unhealthy"),
				filters.Arg("status", "running"),
				//	filters.Arg("label", "xxxx=true"),
			),
		},
	)
	if err != nil {
		panic(err)
	}

	for _, ctr := range containers {
		//	重启异常容器
		// 添加判断条件，限制每天只能重启指定最大次数
		//if rc := containerRestart.getRestartCount(ctr.ID); rc >= sc.MaxRestart {
		//	log.Printf("%s %s (status: %s) 重启次数已达到最大限制 %d\n", ctr.Names, ctr.ID, ctr.Status, sc.MaxRestart)
		//	continue
		//} else {
		//	log.Printf("%s %s (status: %s) 发现unhealthy容器，进行第 %d 次重启\n", ctr.Names, ctr.ID, ctr.Status, rc+1)
		//}

		if !restartRecords.Check(ctr.ID) {
			if err := apiClient.ContainerRestart(context.Background(), ctr.ID, container.StopOptions{}); err != nil {
				log.Println(err)
			}
			restartRecords.Add(ctr.ID, 0, time.Now())
		}

		if restartRecords.Check(ctr.ID) {
			r := restartRecords.Get(ctr.ID)
			expireT := r.RestartTime.Add(time.Second * time.Duration(sc.ResetBackoff))
			// todo: 阻塞问题，是否会多次运行问题
			if time.Now().Before(expireT) {
				sleep := math.Min(sc.MaximumBackoff, sc.BaseBackoff*math.Exp2(float64(r.RestartCount)))
				time.Sleep(time.Duration(sleep) * time.Second)

				if err := apiClient.ContainerRestart(context.Background(), ctr.ID, container.StopOptions{}); err != nil {
					log.Println(err)
				}

				//	指数增加
				r.RestartCount++
				r.RestartTime = time.Now()

			} else {
				// 记录重置
				r.RestartCount = 0
				r.RestartTime = time.Now()
				if err := apiClient.ContainerRestart(context.Background(), ctr.ID, container.StopOptions{}); err != nil {
					log.Println(err)
				}
			}

		}
	}
}

func autoHeal() {
	// 每10秒调用一次 check 函数
	interval := time.Duration(sc.Interval) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// 保持主函数一直运行
	for {
		select {
		case <-ticker.C: // 每次 ticker 间隔到达时触发
			autoCheck() // 执行检查函数
		}
	}
}

func setupRouter() *gin.Engine {
	var LogFormatter = func(param gin.LogFormatterParams) string {
		var statusColor, methodColor, resetColor string
		if param.IsOutputColor() {
			statusColor = param.StatusCodeColor()
			methodColor = param.MethodColor()
			resetColor = param.ResetColor()
		}

		if param.Latency > time.Minute {
			param.Latency = param.Latency.Truncate(time.Second)
		}
		return fmt.Sprintf("%v [GIN] |%s %3d %s| %13v | %15s |%s %-7s %s %#v\n%s",
			param.TimeStamp.Format("2006/01/02 15:04:05"),
			statusColor, param.StatusCode, resetColor,
			param.Latency,
			param.ClientIP,
			methodColor, param.Method, resetColor,
			param.Path,
			param.ErrorMessage,
		)
	}

	// Disable Console Color
	// gin.DisableConsoleColor()
	//r := gin.Default()

	//自定义日志格式
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(gin.LoggerWithFormatter(LogFormatter))

	// Ping test
	r.GET("/ping", func(c *gin.Context) {
		c.String(http.StatusOK, "pong")
	})

	//// Get container value
	//r.GET("/container/:name", func(c *gin.Context) {
	//	name := c.Params.ByName("name")
	//	value, ok := containerRestart.getContainerByName(name)
	//	if ok {
	//		c.JSON(http.StatusOK, value)
	//	} else {
	//		c.JSON(http.StatusOK, gin.H{"container": name, "status": "no value"})
	//	}
	//})
	//
	//// Get container list
	//r.GET("/containers", func(c *gin.Context) {
	//	if len(containerRestart) == 0 {
	//		c.JSON(http.StatusOK, gin.H{"status": "no value"})
	//	} else {
	//		c.JSON(http.StatusOK, containerRestart)
	//	}
	//
	//})

	return r
}

func init() {
	restartRecords = new(RestartRecords)
}

func main() {
	// 加载配置
	loadConfig()

	// 自动检测unhealthy容器
	go autoHeal()

	// 启动服务
	r := setupRouter()
	// Listen and Server in 0.0.0.0:8080
	r.Run(":" + sc.Port)
}
