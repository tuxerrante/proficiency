# Security Policy

## Supported Versions

| Version  | Supported          |
| -------- | ------------------ |
| latest   | :white_check_mark: |
| < latest | :x:                |

Proficiency is currently in pre-release (v0.x). Only the latest version on the
`main` branch receives security fixes.

## Reporting a Vulnerability

If you discover a security vulnerability, please report it responsibly using
one of the following methods:

- **Email**: Send details to **security@proficiency-project.dev**
- **GitHub Security Advisory**: Open a [private security advisory](https://github.com/tuxerrante/proficiency/security/advisories/new) on this repository

Please include:

1. A description of the vulnerability
2. Steps to reproduce
3. Affected versions
4. Any potential impact assessment

**Do not open a public GitHub issue for security vulnerabilities.**

## Responsible Disclosure Timeline

- We will acknowledge receipt of your report within **3 business days**.
- We will provide an initial assessment within **10 business days**.
- We aim to release a fix within **90 days** of the initial report.
- We will coordinate with you on public disclosure timing.

## Security Scanning

This project runs automated security scanning in CI on every push to `main`,
on every pull request, and on a weekly schedule. The following tools are used:

- **gosec** -- Static analysis for Go security issues
- **govulncheck** -- Checks Go dependencies against the Go vulnerability database
- **trivy** -- Filesystem vulnerability scanner for dependencies and misconfigurations

See `.github/workflows/security.yml` for details.
