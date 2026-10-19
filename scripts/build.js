// Cross-compiles the Go binary for every supported platform into dist/.
// Requires Go on the build machine only; end users just need Node.
const { execFileSync } = require("node:child_process");
const fs = require("node:fs");
const path = require("node:path");

const root = path.join(__dirname, "..");
const dist = path.join(root, "dist");
const targets = [
  ["linux", "amd64"],
  ["linux", "arm64"],
  ["darwin", "amd64"],
  ["darwin", "arm64"],
  ["windows", "amd64"],
  ["windows", "arm64"],
];

fs.rmSync(dist, { recursive: true, force: true });
for (const [goos, goarch] of targets) {
  const dir = path.join(dist, `${goos}-${goarch}`);
  fs.mkdirSync(dir, { recursive: true });
  const out = path.join(dir, goos === "windows" ? "sbsync.exe" : "sbsync");
  console.log(`build ${goos}/${goarch}`);
  execFileSync("go", ["build", "-trimpath", "-ldflags", "-s -w", "-o", out, "."], {
    cwd: root,
    stdio: "inherit",
    env: { ...process.env, GOOS: goos, GOARCH: goarch, CGO_ENABLED: "0" },
  });
}
