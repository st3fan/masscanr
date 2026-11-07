FROM alpine:3.24.4@sha256:93b41ef40b1677fce8fcc64ff4af09daddfacf5040afde618bc46c1e2d1cc7d4
RUN apk add --no-cache 'masscan~1.3.2' coreutils
COPY native.test /usr/local/bin/native.test
ENV MASSCAN_TEST_BINARY=/usr/bin/masscan \
    MASSCAN_TEST_STDBUF=/usr/bin/stdbuf \
    MASSCAN_TEST_ALPINE=1
ENTRYPOINT ["/usr/local/bin/native.test", "-test.v", "-test.run", "^(TestAlpineRuntime|TestMasscanConfiguration|TestMasscanListOutput|TestMasscanStreamsBeforeInputEOF)$"]
