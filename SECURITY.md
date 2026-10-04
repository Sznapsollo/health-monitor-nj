# Security policy

## Reporting a vulnerability

Please do not open a public issue for a security problem. Report it privately
through GitHub instead: the repository's **Security** tab, then **Report a
vulnerability**. Include what you found, how to reproduce it, and the version
(`/api/server` or the `VERSION` file).

You should get a reply within a week. Fixes go into the latest release only.

## Running it safely

- Always set `HM_PASS`. The server refuses to start without one unless
  `HM_OPEN=1`, which is meant only for a machine nobody else can reach.
- The UDP intake has no authentication. Expose port 8082 only to the hosts
  that send to it.
- Put the dashboard behind HTTPS (a reverse proxy) when it is reachable beyond
  a trusted network.
