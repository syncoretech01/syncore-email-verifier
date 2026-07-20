// PM2 process definition for the Syncore Email Verifier.
//
// Configuration is read from the process environment. Prefer sourcing a
// vault-populated env file before `pm2 start` (so secrets never live in this
// file); the non-secret defaults below document the expected keys. Do NOT commit
// a real SYNCORE_VERIFIER_AUTH_TOKEN.
//
//   set -a && . /etc/syncore/email-verifier.env && set +a
//   pm2 start deploy/ecosystem.config.js
module.exports = {
  apps: [
    {
      name: 'syncore-email-verifier',
      script: '/opt/syncore/email-verifier/apiserver',
      exec_mode: 'fork',
      instances: 1,
      autorestart: true,
      max_restarts: 10,
      restart_delay: 5000,
      kill_timeout: 30000, // allow the graceful SIGTERM drain to finish
      env: {
        // Loopback by default. On a private subnet, bind the private address AND
        // set SYNCORE_VERIFIER_AUTH_TOKEN (startup fails on a non-loopback bind
        // without a token).
        SYNCORE_VERIFIER_BIND_ADDR: '127.0.0.1:8080',
        SYNCORE_VERIFIER_SMTP_ENABLED: 'true',
        // SYNCORE_VERIFIER_AUTH_TOKEN: '<inject from vault>',
        // SYNCORE_VERIFIER_CACHE_TTL: '30m',
        // SYNCORE_VERIFIER_BATCH_MAX_ITEMS: '100',
        // SYNCORE_VERIFIER_BATCH_CONCURRENCY: '10',
      },
    },
  ],
};
