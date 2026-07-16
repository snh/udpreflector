# Repository Instructions

- Use Australian English throughout this repository, including documentation, user-interface copy, logs, comments, test descriptions, and new identifiers.
- Prefer spellings such as `behaviour`, `authorise`, `authorisation`, `recognise`, `initialise`, and `centre`.
- Preserve spellings mandated by programming languages, libraries, protocols, standards, command-line flags, and externally defined APIs or data formats. For example, do not alter CSS properties such as `color` or protocol field names solely to change their spelling. Use Australian English for application-owned API and data-format names, including JSON fields.

## Sandbox execution

- These sandbox instructions apply only to local VS Code sessions running on macOS with `limactl` available.
- In GitHub Copilot coding agents, Codespaces, CI, and GitHub Actions, run commands directly in the provided environment. Do not install, start, or use Lima.
- Run all shell commands, package installations, builds, tests, and application processes in the Alpine Linux Lima VM named `<projectname>-sandbox`, not directly on the macOS host.
- Reuse the existing `<projectname>-sandbox` instance whenever it exists. Do not delete, replace, or recreate it unless the user explicitly instructs you to do so. If the existing instance is stopped, start that instance without deleting it.
- If `<projectname>-sandbox` does not exist, create and start it from the repository root on the macOS host with the following commands. `--tty=false` is required for unattended startup, and `--name=<projectname>-sandbox` is required because current Lima versions accept only the template as the positional `start` argument. The writable repository is mounted at the same absolute path in the guest.

	```sh
	if ! limactl list --quiet | grep -Fxq "<projectname>-sandbox"; then
		limactl start --tty=false --name="<projectname>-sandbox" --mount-only "$(pwd):w" template:alpine
	fi
	```

- Install missing guest tools with `sudo apk add --no-cache <packages>`.
- Lima forwards the guest listener to host loopback; the guest IP is not assumed to be host-routable.
