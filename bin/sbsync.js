#!/usr/bin/env node
// Launcher: runs the prebuilt Go binary matching this platform.
const { spawnSync } = require("node:child_process");
const path = require("node:path");

const os = { linux: "linux", darwin: "darwin", win32: "windows" }[process.platform];
const arch = { x64: "amd64", arm64: "arm64" }[process.arch];
if (!os || !arch) {
  console.error(`sbsync: unsupported platform ${process.platform}/${process.arch}`);
  process.exit(1);
}

const exe = path.join(__dirname, "..", "dist", `${os}-${arch}`, os === "windows" ? "sbsync.exe" : "sbsync");
const res = spawnSync(exe, process.argv.slice(2), { stdio: "inherit" });
if (res.error) {
  console.error(`sbsync: cannot run ${exe}: ${res.error.message}`);
  process.exit(1);
}
process.exit(res.status ?? 1);
