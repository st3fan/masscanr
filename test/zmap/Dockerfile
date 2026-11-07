FROM alpine:3.24.4@sha256:93b41ef40b1677fce8fcc64ff4af09daddfacf5040afde618bc46c1e2d1cc7d4
RUN apk add --no-cache zmap=4.3.4-r0
COPY native.test /usr/local/bin/native.test
ENV ZMAP_TEST_BINARY=/usr/sbin/zmap
ENTRYPOINT ["/usr/local/bin/native.test", "-test.v", "-test.run", "^TestZMap"]
