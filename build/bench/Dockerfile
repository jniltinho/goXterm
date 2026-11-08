# Benchmark runner: the Go toolchain image plus a headless X server, input/screenshot tools and the
# software GL / xkbcommon libraries the Rust (egui) app needs, so both apps run in one clean box.
FROM rustxterm-go-linux
RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
      xvfb xdotool imagemagick dbus dbus-x11 libxkbcommon-x11-0 libgl1 libegl1 libgl1-mesa-dri \
      libxcursor1 libxi6 libxrandr2 fonts-dejavu-core procps bc \
    && rm -rf /var/lib/apt/lists/*
