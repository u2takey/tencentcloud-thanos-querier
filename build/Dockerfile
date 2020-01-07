FROM alpine:3.6

RUN apk update && apk add ca-certificates && \
    apk add tzdata && \
    ln -sf /usr/share/zoneinfo/Asia/Shanghai /etc/localtime && \
    echo "Asia/Shanghai" > /etc/timezone

COPY ./tq /tq
COPY ./qcloud.yml /qcloud.yml

EXPOSE 10901

ENTRYPOINT ["/tq"]
CMD ["store", "--log.level=debug"]