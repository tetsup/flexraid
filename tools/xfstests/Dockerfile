FROM golang:1.25-bookworm

ARG XFSTESTS_REF=master
ENV DEBIAN_FRONTEND=noninteractive
ENV XFSTESTS_DIR=/opt/xfstests

RUN apt-get update && apt-get install -y --no-install-recommends \
    acl attr automake bc build-essential fio gawk git kmod libaio-dev \
    libacl1-dev libattr1-dev libcap-dev libgdbm-dev libgdbm-compat-dev \
    libtool libtool-bin liburing-dev libuuid1 psmisc python3 python3-pip \
    quota rsync sed sqlite3 uuid-dev xfsprogs xfslibs-dev fuse3 util-linux \
    e2fsprogs ca-certificates \
    && rm -rf /var/lib/apt/lists/*

RUN git clone --depth 1 --branch "${XFSTESTS_REF}" \
      https://github.com/kdave/xfstests.git "${XFSTESTS_DIR}" \
    && cd "${XFSTESTS_DIR}" \
    && make -j"$(nproc)" \
    && test -x ltp/fsstress \
    && test -x ltp/fsx \
    && test -s tests/generic/group.list

WORKDIR /workspace/flexraid
ENTRYPOINT ["/workspace/flexraid/tools/xfstests/container.sh"]
CMD ["help"]
