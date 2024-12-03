package main

import (
	"errors"
	"fmt"
	"github.com/docker/docker/api/types/filters"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"context"
	"github.com/gin-gonic/gin"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

type Container struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	RestartCount int    `json:"restart_count"`
}

// Containers 自定义类型，并为该类型添加方法
type Containers []Container

// 采用指针接收者避免复制Containers结构体
func (c *Containers) check(id string) bool {
	for _, v := range *c {
		if v.ID == id {
			return true
		}
	}
	return false
}

func (c *Containers) getRestartCount(id string) int {
	for _, v := range *c {
		if v.ID == id {
			return v.RestartCount
		}
	}
	return 0
}

func (c *Containers) getContainer(id string) (*Container, bool) {
	for i := range *c {
		if (*c)[i].ID == id {
			return &(*c)[i], true
		}
	}
	return nil, false
}

func (c *Containers) getContainerByName(n string) (*Container, bool) {
	for i := range *c {
		if (*c)[i].Name == n {
			return &(*c)[i], true
		}
	}
	return nil, false
}

func (c *Containers) updateRestartCount(id string, rc int) error {
	for i := range *c {
		if (*c)[i].ID == id {
			(*c)[i].RestartCount = rc
			return nil
		}
	}
	return errors.New("container not found")
}

// 定义变量
var (
	containerRestart   Containers
	autoHealInterval   = 5
	dockerApiVersion   = "1.39"
	autoHealMaxRestart = 3
)

// 从环境变量加载配置覆盖默认值
func loadEnvVars() {
	if dockerV := os.Getenv("DOCKER_API_VERSION"); dockerV != "" {
		dockerApiVersion = dockerV
	}

	if interval := os.Getenv("AUTOHEAL_INTERVAL"); interval != "" {
		if s, err := strconv.Atoi(interval); err == nil {
			autoHealInterval = s
		}
	}

	if maxRestart := os.Getenv("AUTOHEAL_MAX_RESTART"); maxRestart != "" {
		if s, err := strconv.Atoi(maxRestart); err == nil {
			autoHealMaxRestart = s
		}
	}

}
func serverInfo() {
	var p string
	if port := os.Getenv("PORT"); port != "" {
		p = ":" + port
	} else {
		p = ":8080"
	}

	ginMode := gin.DebugMode
	if mode := os.Getenv("GIN_MODE"); mode != "" {
		ginMode = mode
	}

	log.Printf("检查间隔: %d s, Docker API Version: %s, 每天最大重启次数: %d, 监听端口: %s, GIN模式: %s\n", autoHealInterval, dockerApiVersion, autoHealMaxRestart, p, ginMode)
}

func cleanContainers() {
	log.Println("当前容器列表:", containerRestart)
	containerRestart = containerRestart[:0]
	log.Println("容器列表已清空")
}

func autoCheck() {
	apiClient, err := client.NewClientWithOpts(client.FromEnv, client.WithVersion(dockerApiVersion))
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
		if rc := containerRestart.getRestartCount(ctr.ID); rc >= autoHealMaxRestart {
			log.Printf("%s %s (status: %s) 重启次数已达到最大限制 %d\n", ctr.Names, ctr.ID, ctr.Status, autoHealMaxRestart)
			continue
		} else {
			log.Printf("%s %s (status: %s) 发现unhealthy容器，进行第 %d 次重启\n", ctr.Names, ctr.ID, ctr.Status, rc+1)
		}

		if err := apiClient.ContainerRestart(context.Background(), ctr.ID, container.StopOptions{}); err != nil {
			log.Println(err)
		}

		// 记录重启次数
		if c, ok := containerRestart.getContainer(ctr.ID); ok {
			c.RestartCount += 1
		} else {
			newC := Container{Name: strings.Join(ctr.Names, "")[1:], ID: ctr.ID, RestartCount: 1}
			containerRestart = append(containerRestart, newC)
		}
	}
}

func autoHeal() {
	// 每10秒调用一次 check 函数
	interval := time.Duration(autoHealInterval) * time.Second
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

// 每天定时清理一次
func cronClean() {
	// 获取当前时间
	now := time.Now()
	// 计算下一个午夜12点的时间点
	nextMidnight := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())

	//计算下一个午夜的时间差
	durationUntilMidnight := nextMidnight.Sub(now)

	// 等待下一个午夜12点
	time.Sleep(durationUntilMidnight)

	// 清空容器切片
	cleanContainers()

	// 每天继续重复清空操作
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	//每经过24小时清空容器列表
	for {
		select {
		case <-ticker.C:
			cleanContainers()
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

	// Get container value
	r.GET("/container/:name", func(c *gin.Context) {
		name := c.Params.ByName("name")
		value, ok := containerRestart.getContainerByName(name)
		if ok {
			c.JSON(http.StatusOK, value)
		} else {
			c.JSON(http.StatusOK, gin.H{"container": name, "status": "no value"})
		}
	})

	// Get container list
	r.GET("/containers", func(c *gin.Context) {
		if len(containerRestart) == 0 {
			c.JSON(http.StatusOK, gin.H{"status": "no value"})
		} else {
			c.JSON(http.StatusOK, containerRestart)
		}

	})

	return r
}

func main() {
	// 加载环境变量
	loadEnvVars()

	// 输出配置信息
	serverInfo()

	// 自动检测unhealthy容器
	go autoHeal()

	// 每天重置重启记录
	go cronClean()

	// 启动服务
	r := setupRouter()
	// Listen and Server in 0.0.0.0:8080
	r.Run()

}
