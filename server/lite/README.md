
An all-in-one image for Dex server and its internal workflow backend.

For local SDK and Web development, prefer `dexcli dev`. The lite image remains
the self-contained Docker option for environments that already use containers.

DEX service: http://localhost:8801/
## How to use
```shell
docker run -p 8801:8801 -e AUTO_FIX_WORKER_URL=host.docker.internal --add-host host.docker.internal:host-gateway -it superdurable/dex-server-lite:latest
```

## How to build
Make sure you are at the root directory of this project (parent of current):
```shell
docker build . -t superdurable/dex-server-lite:<yourTag> -f lite/Dockerfile
```

You can use `--platform` to test building for alternate architectues see [--platform](https://docs.docker.com/reference/cli/docker/buildx/build/#platform)
