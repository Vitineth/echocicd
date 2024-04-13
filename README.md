# echo ci/cd

> Echo (Ηχώ), a nymph cursed never to speak except to repeat the words of others

This is a really simple docker based CI/CD based deployer that runs against local Gitea instances.

## Requirements

Docker running on the host(s); a Gitea server; and an etcd cluster. This in theory can support remote registries and
running on different hosts but this is currently completely untested.

## Setup

### Builder (`webhook-server`)

Ensure that your etcd cluster is accessible. Then setup the webhook server which will be responsible for receiving
webhooks from Gitea and then building + publishing the images

```bash
$ echocicd --etcd-endpoints=<endpoints> webhook-server --builder-dir ./builders --allowed-refs-file allowed-refs.json
```

> [!NOTE]
> Endpoints should include the protocol, ie `http://127.0.0.1:2379`

#### `allowed-refs.json`

The `allowed-refs.json` file should contain the git refs that you want to be built when a webhook is received. This
gives you some more granular control over what gets built. For example, to build everything on the main branch

```json
{
  "*": [
    "refs/heads/main"
  ]
}
```

### Deployer (`agent`)

Then you can run the deployer! This is what will actually run the images written by the server.

```bash
$ $ echocicd --etcd-endpoints=<endpoints> agent
```

## Deploy Configs

You can explore the code for the exact schemas for deploy configs, however an example is posted here for reference

```toml
[global]
name = "test-deploy"
repo = "ryan/test-deploy"

[builder]
id = "golang"
exclude = [
    "test-file.txt"
]

[builder.args]
entrypoint = "main.go"

[exec]
args = [
    "-testing"
]
ports = { 1324 = 1343 }
volumes = [
    { readonly = false, host = "/mnt/testing", bindTo = "/demo" }
]
domain = { port = 1343, host = "testing.domain.localhost" }
```

This uses the `golang` builder included in this repository. Most of this should hopefully clear, `exclude` entries will
be written to a `.dockerignore` file prior to building. `builder.args` will be converted to JSON and passed into the
Dockerfile as the argument `BUILDER_ARGS` (ie your Dockerfile should contain `ARG BUILDER_ARGS={}` if you need them) for
use in the builder. If you want to use these in external build scripts, the current recommended way is probably to
either write the content to a file somehow or convert it to an env var using `ENV BUILDER_ARGS_ENV=${BUILDER_ARGS}` (
this is used in the golang builder). Ports are expressed as `internal = external`. And `domain` is not required and will
be written as a label for use with the caddy docker integration described on my blog.

## Builders

Builders in essence are a glorified Dockerfile. They are identified by an ID which should be the folder name and they
should all contain a single `Dockerfile` which will build an image. When invoked, all files in the builder folder are
copied into the root of the cloned repository, and then `docker build` is run. The golang example shows how simple this
can be, this one is kind of overkill as you definitely don't need a node builder script, I was just feeling lazy.

Builders can also require some arguments which can help when building the image. These can be validated using a json
schema included in the builder folder with the name `args.schema.json`. This schema will be run against
the `builder.args` section of the deploy config before build, and the build will be aborted if it doesn't match. 