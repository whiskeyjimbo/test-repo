# test-repo

Prints a greeting.

## Usage

```
test-repo [--help] [--version] [--version-json] [--name=NAME] [--shout] [--quiet] [--config=FILE] [--lang=CODE]
```

- `--help` / `--version`: print usage / version and exit 0.
- `--version-json`: print `{"version":"<version>"}` (JSON-escaped, one line) and
  exit 0. Like `--help` and `--version`, the first of the three wins and
  nothing else is read.
- `--name=NAME`: who to greet (default `world`). First non-empty value wins.
- `--shout`: uppercase the output.
- `--quiet`: print nothing, exit 0.
- `--config=FILE`: read settings from FILE. Exact prefix `--config=`; the first
  non-empty value wins. An empty value counts as not given, and `--config FILE`
  (separate argument) is ignored.
- `--lang=CODE`: greeting language. Codes are exact and case-sensitive:
  `en` (`Hello`) and `es` (`Hola`). Only the greeting word changes; the name
  stays as is. Exact prefix `--lang=`; the first non-empty value wins and later
  ones are not checked. An empty value counts as not given, and `--lang CODE`
  (separate argument) is ignored. It beats the config file's `greeting` key
  (the file is still read and validated).
  Any other code is an error: nothing on stdout, stderr exactly
  `test-repo: lang: unknown code, want en or es`, exit 1 (the code is never
  echoed). The code is checked before the config file is opened, so it wins
  over a bad file. `--help`, `--version` and `--quiet` skip the check.

## Config file

```
greeting = Good morning
```

- Lines are split on `\n`. Surrounding whitespace is trimmed (so CRLF works).
  Blank lines are ignored. No line length limit; no trailing newline needed.
- Every other line is `key=value`, split at the first `=`; key and value are
  trimmed. Keys are case-sensitive. Values are literal (may contain `=`,
  spaces, `%s`).
- Comments are not supported: a line starting with `#` is an error.
- A UTF-8 BOM is not stripped; it becomes part of the first key (unknown key).

Keys:

- `greeting`: the greeting word. Empty or absent means `Hello`.
- `name`: who to greet. Empty or absent means it is not set.
  Precedence: first non-empty `--name=` > non-empty `name` key > `world`.
  A valid `--name` does not skip the file: it is still read and validated.

The file is read only when a greeting will print: `--help`, `--version` and
`--quiet` never open it.

## Errors

Every error prints nothing on stdout, exactly one line on stderr
(`test-repo: config FILE: reason`), and exits 1. The message never echoes file
contents.

- File missing, unreadable, or a directory: the OS error.
- A line with no `=`: `line N`.
- An empty key: `line N`.
- An unknown key: `line N`.
- A repeated key (even with equal or empty values): `line N`, the later line.

## Examples

```
$ printf 'greeting=Hi\n' > c.cfg
$ test-repo --config=c.cfg            # stdout: Hi, world   exit 0
$ test-repo --config=c.cfg --name=Ann # stdout: Hi, Ann     exit 0
$ test-repo --config=c.cfg --shout    # stdout: HI, WORLD   exit 0
$ printf 'greeting=Hi\nname=Ann\n' > n.cfg
$ test-repo --config=n.cfg            # stdout: Hi, Ann     exit 0
$ test-repo --config=n.cfg --name=Bob # stdout: Hi, Bob     exit 0
$ test-repo --config=nope             # stderr: test-repo: config nope: no such file or directory   exit 1
$ test-repo --config=nope --help      # usage, exit 0 (file not opened)
$ test-repo --lang=es                 # stdout: Hola, world exit 0
$ test-repo --lang=es --shout         # stdout: HOLA, WORLD exit 0
$ test-repo --config=c.cfg --lang=es  # stdout: Hola, world exit 0
$ test-repo --lang=fr                 # stderr: test-repo: lang: unknown code, want en or es   exit 1
$ test-repo --lang=fr --help          # usage, exit 0 (code not checked)
```

## serve

```
test-repo serve --port=N [--data=FILE] [--rate=N]
```

Runs a small HTTP server. `serve` is a subcommand only when it is the very
first argument, spelled exactly `serve`; anywhere else (`--name=Ann serve`,
`SERVE`, `--help serve`) the greeting CLI runs as usual.

- `--port=N`: required. Decimal digits only, 0-65535; `0` lets the OS pick a
  free port. Exact prefix `--port=` (`--port N` is an error).
- `--data=FILE`: optional. Keep the greeting counts in FILE (see below). Exact
  prefix `--data=`; an empty value is an error.
- `--rate=N`: optional, default 10. Requests per second allowed per client, 1-1000000,
  decimal digits only (see "Rate limiting"). Exact prefix `--rate=`.
- `--help` anywhere after `serve`: print
  `Usage: test-repo serve --port=N [--data=FILE] [--rate=N]` and exit 0. Nothing else is checked.

The server listens on `127.0.0.1` only (no other address can be chosen), then
prints exactly one line to stdout, with the actual port:

```
listening on 127.0.0.1:PORT
```

Endpoints. Every body is one line of JSON ending in a newline, with
`Content-Type: application/json`. `<`, `>` and `&` in a name are written as
JSON unicode escapes (the `json.Encoder` default), never as raw characters.

| Request | Status | Body |
| --- | --- | --- |
| `GET /healthz` | 200 | `{"status":"ok"}` |
| `GET /greet?name=Ann` | 200 | `{"greeting":"Hello, Ann"}` |
| `GET /greet` or `GET /greet?name=` | 200 | `{"greeting":"Hello, world"}` |
| `GET /greet?name=%zz` (query does not parse) | 400 | `{"error":"invalid query"}` |
| `GET /greet?name=%FF` (not UTF-8, or over 256 bytes) | 400 | `{"error":"invalid name"}` |
| `GET /stats` | 200 | `{"counts":{"Ann":2,"world":1}}` (keys sorted; `{"counts":{}}` before any greeting) |
| more than `--rate` requests per second from one client | 429 | `{"error":"rate limited"}` with `Retry-After: 1` |
| any other path | 404 | |
| another method on `/healthz`, `/greet` or `/stats` | 405 | |

The name is the first `name` value, URL-decoded. It always uses the plain
greeting (`--lang`, `--shout` and config files do not apply).

### Rate limiting

Each client has a token bucket holding `--rate` tokens, refilled at `--rate`
tokens per second up to that cap. A client is the host part of the connection's
address (the port is dropped; IPv4 and IPv6 hosts are separate clients).
`X-Forwarded-For` and other proxy headers are ignored. Every request except
path `/healthz` takes one token, whatever its method or status, so `HEAD`,
`/stats`, 404 and 400 responses count too. With no token the answer is 429
`{"error":"rate limited"}` with `Retry-After: 1`, and nothing is counted.
`/healthz` is never limited. A bucket that has refilled to full is forgotten, and a client that comes
back starts with a full bucket, which is exactly what it would have had.

### Counting and `--data`

Each `/greet` that answers 200 adds 1 to the count for the name it greeted
(`world` when none was given); a `HEAD /greet` that answers 200 counts too.
400, 404, 405 and 429 responses add nothing. Without `--data` the counts live in
memory and start empty on every run.

With `--data=FILE` the counts are loaded before the server listens and the
file is rewritten (to a temp file in the same directory, then renamed over
FILE, so a reader never sees a partial file) before each 200 is sent:

```
{"counts":{"Ann":2,"world":1}}
```

- A missing FILE starts with no counts and is created on the first greeting.
- A valid file is one object with the single key `counts`, mapping strings to
  non-negative integers, with only whitespace after it. Anything else (empty
  file, `null`, extra keys, `1.5`, `-1`, trailing data) is refused with
  `test-repo: serve: data FILE: invalid`, exit 1, and FILE is left untouched.
  A file that cannot be read prints `test-repo: serve: data FILE: <OS error>`.
  File contents are never echoed.
- Before listening, `serve` checks that a file can be created in FILE's folder
  and exits 1 with `test-repo: serve: data FILE: <OS error>` if it cannot. It
  then removes leftover temp files from earlier runs: regular files in that
  folder named exactly `FILE.tmp<digits>` (nothing else, no subfolders). A
  FILE that is refused leaves the folder untouched.
- If the write fails while running, `/greet` answers 500
  `{"error":"storage failed"}`, the count is not kept and the server keeps
  running.
- Run one process per file: there is no file locking, so two servers sharing a
  FILE overwrite each other's counts, and startup cleanup assumes no other
  process is writing temp files for FILE.

On SIGTERM or SIGINT the server stops accepting connections, lets in-flight
requests finish for up to 5 seconds, and exits 0 with nothing on stderr. If
requests are still running after 5 seconds it prints
`test-repo: serve: shutdown: <error>` and exits 1.

Errors print nothing on stdout, exactly one line on stderr and exit 1.
Command-line values are never echoed.

```
$ test-repo serve                       # test-repo: serve: --port is required
$ test-repo serve --port=x              # test-repo: serve: --port: want an integer 0-65535
$ test-repo serve --port=0 --port=0     # test-repo: serve: repeated flag
$ test-repo serve --port=0 --bogus      # test-repo: serve: unknown argument
$ test-repo serve --port=0 --data=      # test-repo: serve: --data: empty value
$ test-repo serve --port=0 --rate=0     # test-repo: serve: --rate: want an integer 1-1000000
$ test-repo serve --port=0 --data=/no/such/dir/d.json   # test-repo: serve: data /no/such/dir/d.json: no such file or directory
$ test-repo serve --port=0 --data=ro/d.json             # test-repo: serve: data ro/d.json: permission denied (ro is mode 0500)
$ test-repo serve --port=8080           # port in use: test-repo: serve: listen tcp 127.0.0.1:8080: bind: address already in use
```
