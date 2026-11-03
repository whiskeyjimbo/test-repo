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
test-repo serve --port=N
```

Runs a small HTTP server. `serve` is a subcommand only when it is the very
first argument, spelled exactly `serve`; anywhere else (`--name=Ann serve`,
`SERVE`, `--help serve`) the greeting CLI runs as usual.

- `--port=N`: required. Decimal digits only, 0-65535; `0` lets the OS pick a
  free port. Exact prefix `--port=` (`--port N` is an error).
- `--help` anywhere after `serve`: print `Usage: test-repo serve --port=N` and
  exit 0. Nothing else is checked.

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
| any other path | 404 | |
| another method on `/healthz` or `/greet` | 405 | |

The name is the first `name` value, URL-decoded. It always uses the plain
greeting (`--lang`, `--shout` and config files do not apply).

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
$ test-repo serve --port=8080           # port in use: test-repo: serve: listen tcp 127.0.0.1:8080: bind: address already in use
```
