# 说明

autoheal 该服务参考 [willfarrell/docker-autoheal](https://github.com/willfarrell/docker-autoheal)

基于golang实现

功能:

1. 按照指定间隔时间，自动检测unhealthy容器，进行重启
2. 限制每天每个容器重启次数
3. 提供对外接口，可查询今天重启的容器和指定容器信息

# 环境变量

| 变量名                  | 功能                    | 默认值     |
|----------------------|-----------------------|---------|
| AUTOHEAL_INTERVAL    | 检查间隔(s)               | 5       |
| DOCKER_API_VERSION   | Docker API 版本         | 1.39    |
| AUTOHEAL_MAX_RESTART | 限制每天最大重启次数            | 3       |
| PORT                 | 启动端口                  | :8080   |
| GIN_MODE             | GIN 模式(debug、release) | release |

# 编译

```shell
go build -o autoheal main.go
```

# 构建镜像

```shell
docker build -t autoheal_exporter:v3 .
```

# 启动

```shell
docker run -d --name autoheal_exporter \
--cpus 1 -m 2G \
--log-opt max-size=512m \
--log-opt max-file=3 \
--restart=unless-stopped \
-v /var/run/docker.sock:/var/run/docker.sock \
-h $(hostname) \
autoheal_exporter:v3
```

映射端口

```shell
docker run -d --name autoheal_exporter \
--cpus 1 -m 2G \
--log-opt max-size=512m \
--log-opt max-file=3 \
--restart=unless-stopped \
-v /var/run/docker.sock:/var/run/docker.sock \
-v /etc/hosts:/etc/hosts \
-h $(hostname) \
-p 8080:8080 \
autoheal_exporter:v3
```

# 接口

| 接口                          | 说明         |
|-----------------------------|------------|
| /ping                       | 测试接口       |
| /containers                 | 获取所有容器重启记录 |
| /container/<container-name> | 获取指定容器重启记录 |
