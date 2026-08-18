package investigate

import (
	"fmt"
	"net/url"
	"strconv"
)

func cleanProbeURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("unsupported scheme")
	}
	if u.Host == "" || u.User != nil {
		return "", fmt.Errorf("invalid host")
	}
	cleaned := u.Scheme + "://" + u.Host + u.EscapedPath()
	if u.RawQuery != "" {
		cleaned += "?" + u.RawQuery
	}
	return cleaned, nil
}

func httpProbeArgv(raw string) ([]string, error) {
	cmds, err := probeCommands(raw)
	if err != nil || len(cmds) == 0 {
		return nil, err
	}
	return cmds[0], nil
}

// probeCommands returns exec argv lists to try in order. The URL is passed as a
// separate argument wherever possible so it is not interpolated into code.
// We never download or install a shell into the application container.
func probeCommands(raw string) ([][]string, error) {
	cleaned, err := cleanProbeURL(raw)
	if err != nil {
		return nil, err
	}
	quoted := strconv.Quote(cleaned)
	return [][]string{
		{"/bin/sh", "-c", fmt.Sprintf(httpProbeScript, quoted, quoted, quoted)},
		{"/nodejs/bin/node", "-e", nodeProbeScript, cleaned},
		{"node", "-e", nodeProbeScript, cleaned},
		{"python3", "-c", python3ProbeScript, cleaned},
		{"python", "-c", python2ProbeScript, cleaned},
		{"curl", "-sS", "-o", "/dev/null", "-D", "-", "--connect-timeout", "2", "--max-time", "5", cleaned},
		{"wget", "-S", "-O", "/dev/null", "-T", "2", "--tries=1", cleaned},
	}, nil
}

// nodeProbeScript reads the URL from argv so caller input cannot break out of
// the script. Extra args to `node -e` start at process.argv[1].
const nodeProbeScript = `const u=process.argv[1];
if(!u||(u.indexOf("http://")!==0&&u.indexOf("https://")!==0)){console.error("PROBE_ERROR bad url");process.exit(2)}
const lib=u.startsWith("https:")?require("https"):require("http");
const req=lib.get(u,{timeout:5000},res=>{console.log("HTTP_STATUS",res.statusCode);res.resume();process.exit(0)});
req.on("error",e=>{console.error(e.code||"",e.message);process.exit(1)});
req.on("timeout",()=>{console.error("ETIMEDOUT");req.destroy();process.exit(1)});`

const python3ProbeScript = `import sys,urllib.request,urllib.error
u=sys.argv[1]
try:
 r=urllib.request.urlopen(u,timeout=5)
 print("HTTP_STATUS",r.status)
except urllib.error.HTTPError as e:
 print("HTTP_STATUS",e.code)
`

const python2ProbeScript = `import sys,urllib2
u=sys.argv[1]
try:
 r=urllib2.urlopen(u,timeout=5)
 print("HTTP_STATUS",r.getcode())
except urllib2.HTTPError as e:
 print("HTTP_STATUS",e.code)
`

// httpProbeScript prefers an existing client. If none exists and we are root it
// may install wget, run one GET, then uninstall via trap. It never upgrades
// packages, never runs apt-get update, and never writes into app directories.
const httpProbeScript = `URL=%s
MARKER=/tmp/.crnet-apm-probe.$$
TMPBIN=/tmp/crnet-apm-httpprobe.$$
set +e

cleanup() {
  if [ -f "$MARKER" ]; then
    kind=$(cat "$MARKER" 2>/dev/null)
    case "$kind" in
      apk-wget)
        apk del --quiet wget >/dev/null 2>&1 || true
        ;;
      apt-wget)
        DEBIAN_FRONTEND=noninteractive apt-get remove -y -qq wget >/dev/null 2>&1 || true
        ;;
      microdnf-wget)
        microdnf remove -y wget >/dev/null 2>&1 || true
        ;;
      yum-wget)
        yum remove -y wget >/dev/null 2>&1 || true
        ;;
    esac
    rm -f "$MARKER"
  fi
  rm -f "$TMPBIN"
}
trap cleanup EXIT INT TERM HUP

have_client() {
  command -v wget >/dev/null 2>&1 && return 0
  command -v curl >/dev/null 2>&1 && return 0
  command -v python3 >/dev/null 2>&1 && return 0
  command -v python >/dev/null 2>&1 && return 0
  command -v busybox >/dev/null 2>&1 && return 0
  return 1
}

run_probe() {
  if command -v wget >/dev/null 2>&1; then
    wget -S -O /dev/null -T 2 --tries=1 "$URL"
    return $?
  fi
  if command -v curl >/dev/null 2>&1; then
    curl -sS -o /dev/null -D - --connect-timeout 2 --max-time 5 "$URL"
    return $?
  fi
  if command -v python3 >/dev/null 2>&1; then
    python3 -c 'import urllib.request,urllib.error
u=%s
try:
 r=urllib.request.urlopen(u, timeout=5)
 print("HTTP_STATUS", r.status)
except urllib.error.HTTPError as e:
 print("HTTP_STATUS", e.code)
'
    return $?
  fi
  if command -v python >/dev/null 2>&1; then
    python -c 'import urllib2
try:
 r=urllib2.urlopen(%s, timeout=5)
 print("HTTP_STATUS", r.getcode())
except urllib2.HTTPError as e:
 print("HTTP_STATUS", e.code)
'
    return $?
  fi
  if command -v busybox >/dev/null 2>&1; then
    busybox wget -S -O /dev/null -T 2 "$URL"
    return $?
  fi
  if [ -x "$TMPBIN" ]; then
    "$TMPBIN" -S -O /dev/null -T 2 --tries=1 "$URL"
    return $?
  fi
  return 127
}

if have_client; then
  run_probe
  exit $?
fi

if [ "$(id -u 2>/dev/null || echo 1)" != "0" ]; then
  echo PROBE_ERROR no http client and not root
  exit 2
fi

installed=
if command -v apk >/dev/null 2>&1; then
  if apk add --no-cache --quiet wget >/dev/null 2>&1; then
    echo apk-wget > "$MARKER"
    installed=apk-wget
  fi
elif command -v apt-get >/dev/null 2>&1; then
  if DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends wget >/dev/null 2>&1; then
    echo apt-wget > "$MARKER"
    installed=apt-wget
  fi
elif command -v microdnf >/dev/null 2>&1; then
  if microdnf install -y wget >/dev/null 2>&1; then
    echo microdnf-wget > "$MARKER"
    installed=microdnf-wget
  fi
elif command -v yum >/dev/null 2>&1; then
  if yum install -y wget >/dev/null 2>&1; then
    echo yum-wget > "$MARKER"
    installed=yum-wget
  fi
fi

if [ -z "$installed" ]; then
  echo PROBE_ERROR no http client and install skipped
  exit 2
fi

echo PROBE_INSTALLED "$installed"
run_probe
status=$?
exit $status
`
