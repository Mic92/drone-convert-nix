A conversion extension to run hydra jobsets in drone in dedicated pipeline stages. _Please note this project requires Drone server version 1.4 or higher._

Current status: WIP

## Installation

Create a shared secret:

```console
$ openssl rand -hex 16
bea26a2221fd8090ea38720fc445eca6
```

This plugin needs access to the drone server api:
- `DRONE_SERVER`: url to drone server
- `DRONE_TOKEN`: api token to drone server

Download and run the plugin:

```console
$ docker run -d \
  --publish=3000:3000 \
  --env=DRONE_DEBUG=true \
  --env=DRONE_SECRET=bea26a2221fd8090ea38720fc445eca6 \
  --env=DRONE_SERVER=https://1.2.3.4:8080 \
  --env=DRONE_TOKEN=drone_api_token \
  --restart=always \
  --name=converter Mic92/drone-convert-nix
```

Update your Drone server configuration to include the plugin address and the shared secret.

```text
DRONE_CONVERT_PLUGIN_ENDPOINT=http://1.2.3.4:3000
DRONE_CONVERT_PLUGIN_SECRET=bea26a2221fd8090ea38720fc445eca6
