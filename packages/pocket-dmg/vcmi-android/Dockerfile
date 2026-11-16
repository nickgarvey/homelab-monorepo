# Build environment for VCMI's Android APK, mirroring the ubuntu-latest GitHub runner used by VCMI CI.
FROM ubuntu:24.04
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends \
      build-essential cmake ninja-build git curl ca-certificates unzip xz-utils \
      python3 pipx openjdk-17-jdk-headless file \
    && rm -rf /var/lib/apt/lists/*
ENV ANDROID_HOME=/opt/android-sdk JAVA_HOME=/usr/lib/jvm/java-17-openjdk-amd64 PIPX_BIN_DIR=/usr/local/bin
RUN mkdir -p $ANDROID_HOME/cmdline-tools && cd /tmp \
    && curl -sLo tools.zip https://dl.google.com/android/repository/commandlinetools-linux-13114758_latest.zip \
    && unzip -q tools.zip && mv cmdline-tools $ANDROID_HOME/cmdline-tools/latest && rm tools.zip \
    && yes | $ANDROID_HOME/cmdline-tools/latest/bin/sdkmanager --licenses >/dev/null \
    && $ANDROID_HOME/cmdline-tools/latest/bin/sdkmanager "platforms;android-35" "build-tools;35.0.0" "platform-tools" >/dev/null
RUN PIPX_HOME=/opt/pipx pipx install conan && chmod -R a+rX /opt/pipx && chmod -R a+rwX $ANDROID_HOME
