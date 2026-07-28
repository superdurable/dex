
## Option 1:
You can  provide a volume override for this config for the ../server/docker-compose/docker-compose.yml#L82 container for file:
/dex/config/config_template.yaml

## Option 2
For local docker-compose, you can log into the container: run docker exec -it dex-server /bin/bash to login your container. And edit /dex/config/config_template.yaml

After you changed the config, logout and use docker restart server to restart the container.