# 说明

autoheal 该服务参考 [willfarrell/docker-autoheal](https://github.com/willfarrell/docker-autoheal)

基于 golang 实现

功能:

1. 通过配置文件获取启动配置
2. 采用指数级回退延迟机制重启unhealthy容器。

    - 首次unhealthy，直接重启；
    - 连续unhealthy 依次按照(10s,20s,40s...)时间等待后重启，最大值默认300s，五分钟
    - 健康运行 600s 后,重置回退延迟机制，将新的一次unhealthy视为首次。


其中等待时间按照 min(((2^n)*base_backoff), maximum_backoff) 计算，其中，n 会在每次重启后增加 1。

# 配置文件

```yaml
server:
  # 指定 docker api 版本
  docker_api_version: 1.39
  # 检查间隔（s）
  interval: 5
  # 指数退避基本时间 s
  base_backoff: 10
  # 指数退避最大时间 s
  maximum_backoff: 300
  # 重置回退时间 s
  reset_backoff: 600
```

# 编译

```shell
go build -o autoheal main.go
```

# 构建镜像

```shell
docker build -t autoheal_exporter:v4 .
```

# 启动

**采用默认配置文件**

```shell
docker run -d --name autoheal_exporter \
--cpus 1 -m 2G \
--log-opt max-size=512m \
--log-opt max-file=3 \
--restart=unless-stopped \
-v /var/run/docker.sock:/var/run/docker.sock \
-h $(hostname) \
autoheal_exporter:v4
```

**手动挂载配置文件，并指定**

```shell
docker run -d --name autoheal_exporter \
--cpus 1 -m 2G \
--log-opt max-size=512m \
--log-opt max-file=3 \
--restart=unless-stopped \
-v /var/run/docker.sock:/var/run/docker.sock \
-v /opt/autoheal_exporter/config.yml:/config.yml \
-h $(hostname) \
autoheal_exporter:v4 -c /config.yml
```

# 运行参数

```yaml
Usage of ./autoheal:
   -c string
   config file (default "./config.yml")
```