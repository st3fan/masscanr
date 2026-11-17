FROM fedora:44@sha256:15da97ed9aa94dd3d92c3925eb89520f6c80e07ea7ee24cba848d1f8b6912b3e
RUN dnf -y --releasever=44 --disablerepo='*' --enablerepo=fedora --enablerepo=updates --setopt=fedora.gpgcheck=1 --setopt=updates.gpgcheck=1 install masscan-1.3.2-15.fc44.x86_64 zmap-4.4.0-1.fc44.x86_64 coreutils && dnf clean all
COPY native.test /usr/local/bin/native.test
ENV MASSCAN_TEST_BINARY=/usr/bin/masscan \
    MASSCAN_TEST_STDBUF=/usr/bin/stdbuf \
    MASSCAN_TEST_FEDORA=1 \
    ZMAP_TEST_BINARY=/usr/bin/zmap \
    ZMAP_TEST_PLATFORM=fedora44
ENTRYPOINT ["/usr/local/bin/native.test", "-test.v", "-test.run", "^(TestFedoraRuntime|TestMasscanConfiguration|TestMasscanListOutput|TestMasscanStreamsBeforeInputEOF|TestZMapCapabilities|TestZMapSharding)$"]
