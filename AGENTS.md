# Syncore Email Verifier

## Project purpose

This repository is a customized fork of AfterShip/email-verifier.

It will become a production-grade internal email-verification service for Syncore Tech and will later integrate with Syncore Lead Engine CRM.

## Development rules

- Do not develop directly on the main branch.
- Preserve the upstream MIT licence and attribution.
- Do not introduce paid dependencies unless explicitly approved.
- Do not expose credentials, passwords, proxy URLs, or API keys.
- Store configuration in environment variables.
- Keep tests deterministic.
- Tests must not call public email providers.
- Do not classify SMTP timeouts as invalid.
- Do not classify temporary network failures as invalid.
- Use structured JSON responses.
- Preserve raw verification evidence.
- Prefer small, reviewable changes.
- Before major implementation, write a plan and wait for approval.
- Run formatting and tests after implementation.
- Document all new environment variables and API behavior.

## Core classifications

### valid

The recipient mailbox was explicitly accepted by the receiving mail server.

### invalid

The address has invalid syntax, the domain cannot receive mail, or the recipient was explicitly and permanently rejected.

### risky

The address is disposable, role-based, catch-all, disabled, full, or has another known deliverability concern.

### unknown

The result is inconclusive because of a timeout, connection failure, provider block, rate limit, temporary SMTP rejection, or another technical limitation.

## Important current behavior

- Gmail SMTP verification works from the local development connection.
- Some Microsoft, Yahoo, and Apple SMTP connections time out over IPv4.
- Those timeouts must remain unknown.
- The current API server is only a reference implementation.
- The existing Yahoo live test is unreliable and must be replaced or isolated.
- SMTP configuration is currently hardcoded in cmd/apiserver/main.go.
