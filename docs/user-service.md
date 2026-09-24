# Install rusui as a user service

`rusui setup plan` shows the host changes. `rusui setup apply` creates the
state files and installs a service for the current user. Run setup from the
same rusui binary location you want the service to use:

```sh
rusui setup plan
rusui setup apply
```

The service binds to `127.0.0.1:8080` by default. Choose another loopback
port with `-addr` when that port is already in use:

```sh
rusui setup plan -addr 127.0.0.1:18080
rusui setup apply -addr 127.0.0.1:18080
```

Setup installs a launchd user agent on macOS or a `systemd --user` unit on
Linux. Linux setup enables user lingering so systemd starts the user manager
at boot and keeps it running after logout. Both services run the selected
rusui binary with the managed database, policy, and environment file. The
service definition is stored in the current user's service directory, and
setup restricts it to `127.0.0.1` or `localhost` so the generated TLS
certificate matches the address. On macOS, service output is written to
`rusui.log` and `rusui.err` inside the private state directory. Linux output
is available through the user's systemd journal.

The service uses values from `rusui.env`; shell variables exported only for
the `setup apply` command are not saved for later service starts. Add external
credentials to `rusui.env` before applying setup. Setup creates the file with
mode `0600` and leaves those keys empty for the operator to fill. Values may
be unquoted or surrounded by matching single or double quotes; shell expansion
is not evaluated.

`setup apply` is safe to repeat. It keeps an unchanged service running and
restarts it when the binary, policy, environment, TLS material, address, or
service definition changes. It exits non-zero while
`diagnose` reports blockers such as missing GitHub credentials, model access,
or a guest image; the service may still be installed and available for local
administration. Review those items with `rusui diagnose` and the
`needs-you` lines from setup output.

The setup-managed service file and generated Linux environment file have mode
`0600`. The macOS service file contains the environment values needed by
rusui, including generated credentials. On Linux, the environment file next to
the unit is normalized from `rusui.env`; the source remains mode `0600` inside
the `0700` state directory, and secrets are not copied into the unit itself.
Do not edit the service or generated environment file by hand; rerun
`setup apply` to update them. Setup refuses to overwrite or remove unmanaged
service files.

To stop and uninstall the service while keeping the database, policy,
credentials, TLS material, and logs, run:

```sh
rusui setup remove-service
```

Run `setup apply` again to reinstall it. `remove-service` does not delete the
state directory or disable Linux user lingering. Lingering is an account-wide
setting and may be needed by other systemd user services; manage it separately
with `loginctl disable-linger` if no user services need it.
