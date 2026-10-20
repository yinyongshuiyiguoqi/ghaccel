# Security

ghaccel writes the system hosts file and therefore runs with administrator/root
privileges when applying changes.

## Design boundaries

- It does not install a root certificate.
- It does not intercept, decrypt, or forward TLS traffic.
- It does not change the system proxy, PAC, routing table, or network drivers.
- It writes only inside a clearly marked block; all other bytes are preserved.
- Backups are created before the first modification and before every change.

## Reporting

If you find a vulnerability, open a private security advisory on GitHub rather
than a public issue. Include the version, OS, and reproduction steps.
