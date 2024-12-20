FROM alpine:3.18.3
# 维护者信息
LABEL authors="lglaboy" \
      description="auto restart unhealth container"

# 设置时区，需要安装tzdata
ENV TZ=Asia/Shanghai \
	GIN_MODE=release

# apk add 提速
RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.aliyun.com/g' /etc/apk/repositories \
    && apk add tzdata --no-cache

WORKDIR /

# autoheal 在宿主机上拥有可执行权限，不需要再次授权
COPY autoheal config.yml /

HEALTHCHECK --interval=5s --timeout=3s --start-period=5s \
CMD pgrep autoheal || exit 1

ENTRYPOINT ["/autoheal"]