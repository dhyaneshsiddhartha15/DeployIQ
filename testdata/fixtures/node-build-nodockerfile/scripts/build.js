// A stand-in for a real bundler: it requires a devDependency, so the build
// fails if the build stage installs production dependencies only.
const fs = require("fs");
const path = require("path");
const semver = require("semver");

if (!semver.valid("1.0.0")) throw new Error("semver missing");

const out = path.join(__dirname, "..", "dist");
fs.mkdirSync(out, { recursive: true });
fs.writeFileSync(
  path.join(out, "server.js"),
  `const http = require("http");
const ms = require("ms");
const port = process.env.PORT || 3000;
http.createServer((_req, res) => res.end("built " + ms(60000))).listen(port);
`
);
console.log("built dist/server.js");
