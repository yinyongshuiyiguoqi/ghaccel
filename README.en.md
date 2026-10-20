# ghaccel

A GitHub-only accelerator using smart DNS and the hosts file. No reverse proxy,
no root certificate, no system proxy, no traffic interception.

## How it works

1. Resolve GitHub domains through DoH (AliDNS, DNSPod) and built-in seed IPs.
2. Probe every candidate with TCP connect, TLS handshake using the exact SNI,
   and an optional HTTP HEAD request.
3. Pick the lowest-scoring verified IP and write it into a marked block in the
   hosts file, with backup, atomic replace, and DNS cache flush.
4. Optionally refresh every 15 minutes.

## Install

```bash
go install github.com/yinyongshuiyiguoqi/ghaccel/cmd/ghaccel@latest
```

## Usage

```bash
ghaccel init
ghaccel doctor
ghaccel once --dry-run -v
# elevated terminal:
ghaccel once
ghaccel start
ghaccel status
ghaccel stop
ghaccel restore
```

## Limits

- hosts has no wildcards; `*.github.io` needs explicit `extra_domains`.
- Browser Secure DNS can bypass hosts.
- IPv6 is not covered by default.
- It cannot bypass IP-level blocking.

## License

MIT
