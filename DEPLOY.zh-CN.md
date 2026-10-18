# 部署与下游对接说明（xiaomuclaw fork）

> 本文件记录**自建部署**与**下游对接**的约定。凡是改部署方式、改 compose、升级版本，都请先读这份，
> 并保持第 4 节列出的「不能变」的东西——那些一变，下游对接就会断。

## 1. 部署形态

| 项 | 值 |
|---|---|
| 服务器 | `152.53.170.228`，部署目录 `/opt/grok2api/`（**只放 `compose.yaml` + `config.yaml`，不放源码**） |
| 镜像 | 服务器本地构建，tag 约定 **`grok2api:xiaomu-<main 的 8 位短哈希>`**（哈希即本仓库 main 的提交，便于对照版本） |
| 容器名 | `grok2api`（`compose.yaml` 里 `container_name` 固定） |
| 网络 | `grok2api_default`（compose 项目默认网络，由部署目录名 `grok2api` 决定） |
| 端口 | `127.0.0.1:8000 -> 8000`（只绑回环；公网经 nginx `grok.xmxcode.com`） |
| 数据 | 命名卷 `grok2api-data` -> `/app/data`（**账号池 `backend.db` 在里面，重建容器不会动它**） |
| 配置 | `./config.yaml` 只读挂载到 `/run/grok2api/config.yaml`（改配置无需重建镜像） |

## 2. 升级到新版本的标准步骤

```bash
# 1) 在服务器上拉一份源码（用完即删，不在服务器留备份）
rm -rf /tmp/g2a-build
git clone --depth 1 --branch main https://github.com/xiaomuclaw/grok2api /tmp/g2a-build
SHORT=$(git -C /tmp/g2a-build rev-parse --short=8 HEAD)

# 2) 构建镜像（多阶段：pnpm 前端 + Go 后端）
cd /tmp/g2a-build && docker build -t grok2api:xiaomu-$SHORT .

# 3) 切换镜像并重建容器（数据卷与网络都不动）
cd /opt/grok2api
sed -i "s|image: grok2api:xiaomu-.*|image: grok2api:xiaomu-$SHORT|" compose.yaml
docker compose up -d grok2api

# 4) 清理临时源码
rm -rf /tmp/g2a-build
```

回滚同理：把 `compose.yaml` 的 image 行改回旧 tag，再 `docker compose up -d grok2api`（所以旧镜像要留着）。

## 3. 下游对接（new-api）

new-api 与 grok2api **不在同一张 Docker 网络**，需要先把它接进来（只做一次）：

```bash
docker network connect grok2api_default new-api
```

然后在新-api 的渠道里填：

```
http://grok2api:8000
```

- **不要带 `/v1`**：new-api 的 OpenAI 类型渠道会自己补 `/v1/...`，带上会变成 `/v1/v1/...`。
- **不要填容器 IP**（例如 `http://172.17.0.6:8000`）：容器一重建 IP 就变，渠道会静默变成 500
  `upstream error: do request failed`。用容器名 `grok2api` 才不受重建影响。
- 验证：new-api 渠道页点「测试」，或在服务端跑
  `GET /api/channel/test/<渠道id>?model=grok-4.5`，返回 `{"success":true}` 即打通。

## 4. 不能变的东西（变了对接就断）

1. **容器名保持 `grok2api`**（`compose.yaml` 的 `container_name`）。
2. **网络名保持 `grok2api_default`**：它由部署目录名 `/opt/grok2api` 决定；别改目录名，也别在 compose 里另起网络名。
3. **不要用 `docker compose down`**：`down` 会删掉 `grok2api_default` 网络，new-api 的网络接入会一起丢，
   必须重新执行一次 `docker network connect grok2api_default new-api`。
   日常升级只用 `docker compose up -d grok2api`（重建容器不会动网络，对接不受影响）。
4. 端口映射保持 `127.0.0.1:8000:8000`（nginx 与同机其他服务依赖它）。

> 想彻底免疫第 3 条，可以把 `compose.yaml` 的网络声明为固定名字的外部网络，
> 这样 compose 不再创建/删除它：
>
> ```yaml
> networks:
>   default:
>     name: grok2api_default
>     external: true
> ```
>
> 代价是首次部署前要先 `docker network create grok2api_default`（网络不存在时 `up -d` 会直接报错）。

## 5. 版本对照

- 服务器上跑的镜像 tag 里那串短哈希，就是本仓库 `main` 的提交：`grok2api:xiaomu-03bc5b00` 对应提交 `03bc5b00`。
- 容器内 `/app/VERSION` 是上游版本号（如 `v3.1.6`），与仓库根目录的 `VERSION` 文件一致。
- 核对命令：`docker exec grok2api cat /app/VERSION`、`docker inspect grok2api --format '{{.Config.Image}}'`、
  服务器上 `git -C /tmp/g2a-build rev-parse HEAD`（或 GitHub 上仓库首页）。
