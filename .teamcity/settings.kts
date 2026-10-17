import jetbrains.buildServer.configs.kotlin.*

version = "2025.11"

project {
    description = "构建并启动曼波 GPT-SoVITS、视频配音流水线与 edge-media 编排，发布内网端口供 252 的 /media/* 调用。"
    buildType(Deploy252Cluster)
    buildType(DeployTianjinMedia)
    buildType(DeployTianjinSub2API)
}

object Deploy252Cluster : BuildType({
    name = "Deploy 252 Cluster"
    description = "在 252 Docker Agent 上构建镜像并以单节点蓝绿方式部署 Sub2API，18080 由稳定 Nginx 入口承载；候选先在备用端口验证，失败不影响现有实例。"

    vcs {
        root(DslContext.settingsRoot)
        checkoutMode = CheckoutMode.ON_AGENT
    }

    requirements {
        equals("teamcity.agent.name", "ip_172.19.0.1")
    }

    params {
        param("env.DEPLOY_DIR", "/opt/sub2api")
        param("env.SUB2API_REPLICAS", "1")
        param("env.CANARY_PORT", "auto")
        param("env.CANARY_OBSERVE_SECONDS", "20")
        param("env.IMAGE_REPOSITORY", "zjarlin/sub2api")
        param("env.EDGE_VISION_IMAGE_REPOSITORY", "zjarlin/edge-vision")
        param("env.EDGE_LAYA_IMAGE_REPOSITORY", "zjarlin/edge-laya")
        param("env.EDGE_MEDIA_IMAGE_REPOSITORY", "zjarlin/edge-media")
        param("env.EDGE_MAIL_IMAGE_REPOSITORY", "zjarlin/edge-mail")
        param("env.EDGE_MEDIA_ENABLED", "1")
        param("env.EDGE_MAIL_ENABLED", "1")
        // 边缘媒体走网关：GATEWAY_MEDIA_ENABLED=1 让子服务通过 /media/* 暴露。
        param("env.SUB2API_EDGE_MEDIA", "1")
        // 252 只做编排：TTS / 视频配音都转发到天津 GPU 机器。
        // 天津通过 FRP 把 edge-media(28084) 与 gpt-sovits(28085) 暴露到 252 的 frps。
        param("env.MEDIA_TTS_ENABLED", "1")
        // FRP 端口在 252 本机 loopback 上侦听；容器内通过 host.docker.internal 访问。
        param("env.MEDIA_TTS_UPSTREAM_URL", "http://host.docker.internal:28085")
        param("env.MEDIA_DUBBING_ENABLED", "1")
        param("env.MEDIA_DUBBING_COMMAND", "")
        param("env.MEDIA_VIDEO_UPSTREAM_URL", "http://host.docker.internal:28084")
        // 视频生成走网络 API（Seedance 2.0 等），在网关侧账号池配置，不用离线模型。
        // 平台已原生支持 Ark 异步任务协议：/v3/contents/generations/tasks。
        param("env.MEDIA_VIDEO_GENERATION_ENABLED", "0")
        param("env.MEDIA_VIDEO_GENERATION_UPSTREAM_URL", "")
        param("env.EDGE_LAYA_ENABLED", "0")
    }

    maxRunningBuilds = 1

    triggers {
        trigger {
            type = "vcsTrigger"
            param("branchFilter", "+:<default>")
        }
    }

    steps {
        step {
            name = "Build, sync and deploy 252 cluster"
            type = "simpleRunner"
            param("use.custom.script", "true")
            param("script.content", """
                set -euo pipefail

                CHECKOUT="%teamcity.build.checkoutDir%"
                DEPLOY_DIR="%env.DEPLOY_DIR%"
                SHA="%build.vcs.number%"
                SHORT_SHA="${'$'}{SHA:0:12}"
                IMAGE="%env.IMAGE_REPOSITORY%:${'$'}SHORT_SHA"

                echo "##teamcity[progressStart '校验代码']"
                cd "${'$'}CHECKOUT"
                test -x deploy/cluster/deploy-252.sh
                test -f "${'$'}DEPLOY_DIR/.env"
                test -f "${'$'}DEPLOY_DIR/docker-compose.override.yml"
                if [ "%env.EDGE_LAYA_ENABLED%" = "1" ]; then
                  test -s "${'$'}DEPLOY_DIR/edge-laya/models/checkpoint/model.safetensors"
                  test -s "${'$'}DEPLOY_DIR/edge-laya/models/checkpoint/multilingual/model.safetensors"
                fi
                SUB2API_IMAGE="validation" docker compose \
                  --project-name sub2api \
                  --project-directory "${'$'}DEPLOY_DIR" \
                  --env-file "${'$'}DEPLOY_DIR/.env" \
                  -f deploy/docker-compose.yml \
                  -f "${'$'}DEPLOY_DIR/docker-compose.override.yml" \
                  -f deploy/cluster/docker-compose.yml \
                  config >/dev/null
                echo "##teamcity[progressFinish '校验代码']"

                echo "##teamcity[progressStart '构建不可变镜像']"
                docker build \
                  --build-arg VERSION="${'$'}SHORT_SHA" \
                  --build-arg COMMIT="${'$'}SHA" \
                  --build-arg DATE="${'$'}(date -u +%%Y-%%m-%%dT%%H:%%M:%%SZ)" \
                  -t "${'$'}IMAGE" .
                echo "##teamcity[progressFinish '构建不可变镜像']"

                echo "##teamcity[progressStart '同步部署文件']"
                mkdir -p "${'$'}DEPLOY_DIR"
                git archive "${'$'}SHA" | tar -x -C "${'$'}DEPLOY_DIR"
                chmod +x "${'$'}DEPLOY_DIR/deploy/cluster/deploy-252.sh"
                chmod +x "${'$'}DEPLOY_DIR/deploy/cluster/deploy-edge-vision.sh"
                chmod +x "${'$'}DEPLOY_DIR/deploy/cluster/deploy-edge-media.sh"
                chmod +x "${'$'}DEPLOY_DIR/deploy/cluster/deploy-edge-mail.sh"
                chmod +x "${'$'}DEPLOY_DIR/deploy/cluster/deploy-laya.sh"
                echo "##teamcity[progressFinish '同步部署文件']"

                echo "##teamcity[progressStart '部署单节点蓝绿候选']"
                cd "${'$'}DEPLOY_DIR"
                SUB2API_IMAGE="${'$'}IMAGE" \
                  DEPLOY_DIR="${'$'}DEPLOY_DIR" \
                  CANARY_PORT="%env.CANARY_PORT%" \
                  CANARY_OBSERVE_SECONDS="%env.CANARY_OBSERVE_SECONDS%" \
                  SUB2API_REPLICAS="%env.SUB2API_REPLICAS%" \
                  SUB2API_EDGE_MEDIA="%env.SUB2API_EDGE_MEDIA%" \
                  "${'$'}DEPLOY_DIR/deploy/cluster/deploy-252.sh"
                echo "##teamcity[progressFinish '部署单节点蓝绿候选']"

                echo "##teamcity[progressStart '部署边缘视觉服务']"
                EDGE_VISION_IMAGE_REPOSITORY="%env.EDGE_VISION_IMAGE_REPOSITORY%" \
                  DEPLOY_DIR="${'$'}DEPLOY_DIR" \
                  "${'$'}DEPLOY_DIR/deploy/cluster/deploy-edge-vision.sh"
                echo "##teamcity[progressFinish '部署边缘视觉服务']"

                if [ "%env.EDGE_MEDIA_ENABLED%" = "1" ]; then
                  echo "##teamcity[progressStart '部署边缘媒体服务']"
                  EDGE_MEDIA_IMAGE_REPOSITORY="%env.EDGE_MEDIA_IMAGE_REPOSITORY%" \
                    MEDIA_TTS_ENABLED="%env.MEDIA_TTS_ENABLED%" \
                    MEDIA_TTS_UPSTREAM_URL="%env.MEDIA_TTS_UPSTREAM_URL%" \
                    MEDIA_DUBBING_ENABLED="%env.MEDIA_DUBBING_ENABLED%" \
                    MEDIA_DUBBING_COMMAND="%env.MEDIA_DUBBING_COMMAND%" \
                    MEDIA_VIDEO_UPSTREAM_URL="%env.MEDIA_VIDEO_UPSTREAM_URL%" \
                    MEDIA_VIDEO_GENERATION_ENABLED="%env.MEDIA_VIDEO_GENERATION_ENABLED%" \
                    MEDIA_VIDEO_GENERATION_UPSTREAM_URL="%env.MEDIA_VIDEO_GENERATION_UPSTREAM_URL%" \
                    DEPLOY_DIR="${'$'}DEPLOY_DIR" \
                    "${'$'}DEPLOY_DIR/deploy/cluster/deploy-edge-media.sh"
                  echo "##teamcity[progressFinish '部署边缘媒体服务']"
                fi

                if [ "%env.EDGE_MAIL_ENABLED%" = "1" ]; then
                  echo "##teamcity[progressStart '部署边缘邮件服务']"
                  EDGE_MAIL_IMAGE_REPOSITORY="%env.EDGE_MAIL_IMAGE_REPOSITORY%" \
                    DEPLOY_DIR="${'$'}DEPLOY_DIR" \
                    "${'$'}DEPLOY_DIR/deploy/cluster/deploy-edge-mail.sh"
                  echo "##teamcity[progressFinish '部署边缘邮件服务']"
                fi

                if [ "%env.EDGE_LAYA_ENABLED%" = "1" ]; then
                  echo "##teamcity[progressStart '部署 Laya 决策模型']"
                  EDGE_LAYA_IMAGE_REPOSITORY="%env.EDGE_LAYA_IMAGE_REPOSITORY%" \
                    DEPLOY_DIR="${'$'}DEPLOY_DIR" \
                    "${'$'}DEPLOY_DIR/deploy/cluster/deploy-laya.sh"
                  echo "##teamcity[progressFinish '部署 Laya 决策模型']"
                fi

                echo "##teamcity[progressStart '验证 252 入口']"
                curl --fail --silent --show-error --max-time 20 http://127.0.0.1:18080/health >/dev/null
                docker exec edge-vision curl --fail --silent --show-error --max-time 20 http://127.0.0.1:18081/health >/dev/null
                if [ "%env.EDGE_MEDIA_ENABLED%" = "1" ]; then
                  docker exec edge-media curl --fail --silent --show-error --max-time 20 http://127.0.0.1:18083/health >/dev/null
                fi
                if [ "%env.EDGE_MAIL_ENABLED%" = "1" ]; then
                  docker exec edge-mail curl --fail --silent --show-error --max-time 20 http://127.0.0.1:18084/health >/dev/null
                fi
                if [ "%env.EDGE_LAYA_ENABLED%" = "1" ]; then
                  docker exec edge-laya curl --fail --silent --show-error --max-time 20 http://127.0.0.1:18082/health >/dev/null
                fi
                test "${'$'}(docker ps --filter name=sub2api-gateway --filter status=running -q | wc -l | tr -d ' ')" = "1"
                test "${'$'}(docker ps --filter name=edge-vision --filter health=healthy -q | wc -l | tr -d ' ')" = "1"
                if [ "%env.EDGE_MEDIA_ENABLED%" = "1" ]; then
                  test "${'$'}(docker ps --filter name=edge-media --filter health=healthy -q | wc -l | tr -d ' ')" = "1"
                fi
                if [ "%env.EDGE_MAIL_ENABLED%" = "1" ]; then
                  test "${'$'}(docker ps --filter name=edge-mail --filter health=healthy -q | wc -l | tr -d ' ')" = "1"
                fi
                test "${'$'}(docker ps --filter label=com.docker.compose.service=sub2api --filter status=running -q | wc -l | tr -d ' ')" = "1"
                test "${'$'}(docker ps --filter name=sub2api-canary --filter status=running -q | wc -l | tr -d ' ')" = "0"
                echo "##teamcity[progressFinish '验证 252 入口']"
                echo "DEPLOYED ${'$'}IMAGE；各 edge 服务的实际镜像见对应 DEPLOYED 日志"
            """.trimIndent())
        }
    }
})

// 天津海光 DCU 机器：曼波 TTS + 视频配音 + edge-media 编排。
//
// 镜像在天津本机构建：海光 DTK 基础镜像与曼波权重体积很大，跨公网搬运不现实，
// 而源码只有几百 KB，所以这里把源码同步过去后在天津直接 docker build。
//
// 252 与天津私网互不可达，但 252 已配置 cloudflared ProxyCommand 的
// `ssh tianjin-media` 别名。该 build type 复用 252 agent，通过 SSH 在天津执行部署。
object DeployTianjinMedia : BuildType({
    name = "Deploy Media (GPU)"
    description = "构建并启动曼波 GPT-SoVITS、视频配音流水线与 edge-media 编排，发布内网端口供 252 的 /media/* 调用。"

    vcs {
        root(DslContext.settingsRoot)
        checkoutMode = CheckoutMode.ON_AGENT
    }

    requirements {
        // 复用 252 agent：它同时能访问公网与经 cloudflared 访问天津。
        equals("teamcity.agent.name", "ip_172.19.0.1")
    }

    params {
        param("env.TIANJIN_SSH_HOST", "tianjin-media")
        param("env.TIANJIN_DEPLOY_DIR", "/opt/sub2api-tianjin-media")
        param("env.GPT_SOVITS_MODELS_DIR", "/opt/gptsovits-models")
        param("env.GPT_SOVITS_IMAGE", "gpt-sovits:manbo-v6")
        param("env.EDGE_DUB_IMAGE", "edge-dub:tianjin")
        param("env.EDGE_MEDIA_IMAGE", "edge-media:tianjin")
        param("env.EDGE_MEDIA_DATA_DIR", "/opt/edge-media/data")
        param("env.EDGE_DUB_DATA_DIR", "/opt/edge-dub/data")
        param("env.SUB2API_NETWORK", "sub2api_sub2api-network")
        // frpc 跑在天津本机，发布端口只需 loopback。
        param("env.MEDIA_TIANJIN_BIND", "127.0.0.1")
        // 网络视频生成（Seedance 2.0 等）由 252 网关侧账号池承接，天津默认关闭。
        param("env.MEDIA_VIDEO_GENERATION_ENABLED", "0")
        param("env.MEDIA_VIDEO_GENERATION_UPSTREAM_URL", "")
    }

    maxRunningBuilds = 1

    triggers {
        trigger {
            type = "vcsTrigger"
            param("branchFilter", "+:<default>")
        }
    }

    steps {
        step {
            name = "Build and deploy media services"
            type = "simpleRunner"
            param("use.custom.script", "true")
            param("script.content", """
                set -euo pipefail

                CHECKOUT="%teamcity.build.checkoutDir%"
                SHA="%build.vcs.number%"
                SHORT_SHA="${'$'}{SHA:0:12}"
                REMOTE="%env.TIANJIN_SSH_HOST%"
                RDIR="%env.TIANJIN_DEPLOY_DIR%"

                cd "${'$'}CHECKOUT"
                test -x deploy/tianjin/deploy-tianjin-media.sh

                echo "##teamcity[progressStart '同步媒体服务源码']"
                ssh -o BatchMode=yes "${'$'}REMOTE" "mkdir -p '${'$'}RDIR'"
                # 天津只需要 edge-media 与部署脚本；整仓库归档经慢速隧道要传 ~34MB，
                # 只发这两个目录（<0.1MB）即可。
                git archive "${'$'}SHA" edge-media deploy | ssh -o BatchMode=yes "${'$'}REMOTE" "tar -x -C '${'$'}RDIR'"
                ssh -o BatchMode=yes "${'$'}REMOTE" "chmod +x '${'$'}RDIR/deploy/tianjin/deploy-tianjin-media.sh'"
                echo "##teamcity[progressFinish '同步媒体服务源码']"

                echo "##teamcity[progressStart '部署 GPU 媒体服务']"
                ssh -o BatchMode=yes "${'$'}REMOTE" \
                  "REPO_DIR='${'$'}RDIR' \
                   GPT_SOVITS_MODELS_DIR='%env.GPT_SOVITS_MODELS_DIR%' \
                   GPT_SOVITS_IMAGE='%env.GPT_SOVITS_IMAGE%' \
                   EDGE_DUB_IMAGE='%env.EDGE_DUB_IMAGE%' \
                   EDGE_MEDIA_IMAGE='%env.EDGE_MEDIA_IMAGE%' \
                   EDGE_MEDIA_DATA_DIR='%env.EDGE_MEDIA_DATA_DIR%' \
                   EDGE_DUB_DATA_DIR='%env.EDGE_DUB_DATA_DIR%' \
                   SUB2API_NETWORK='%env.SUB2API_NETWORK%' \
                   MEDIA_TIANJIN_BIND='%env.MEDIA_TIANJIN_BIND%' \
                   MEDIA_VIDEO_GENERATION_ENABLED='%env.MEDIA_VIDEO_GENERATION_ENABLED%' \
                   MEDIA_VIDEO_GENERATION_UPSTREAM_URL='%env.MEDIA_VIDEO_GENERATION_UPSTREAM_URL%' \
                   '${'$'}RDIR/deploy/tianjin/deploy-tianjin-media.sh'"
                echo "##teamcity[progressFinish '部署 GPU 媒体服务']"

                echo "##teamcity[progressStart '验证天津媒体服务']"
                ssh -o BatchMode=yes "${'$'}REMOTE" "docker exec edge-media curl --fail --silent --show-error --max-time 20 http://127.0.0.1:18083/health >/dev/null && \
                  docker exec edge-media curl --fail --silent --show-error --max-time 90 \
                    -X POST http://127.0.0.1:18083/tts \
                    -H 'Content-Type: application/json' \
                    -d '{\"text\":\"你好，我是曼波。\",\"language\":\"zh\",\"response_format\":\"wav\"}' \
                    -o /dev/null"
                echo "##teamcity[progressFinish '验证天津媒体服务']"
                echo "DEPLOYED tianjin edge-media=${'$'}SHORT_SHA"
            """.trimIndent())
        }
    }
})

// 天津 Sub2API：把 Sub2API 网关本体与内置适配器部署到天津主机。
//
// 与 DeployTianjinMedia 的区别：那个部署 GPU 媒体三件套（曼波 TTS / 配音 /
// edge-media）；这个部署 Sub2API 主体。
//
// 关键设计：脚本与 compose 完全通用
//   deploy/docker-compose.adapters.yml（单文件，所有适配器按 profile 开关）
//   deploy/cluster/adapters.env.sh     （平台注册表：开关/密钥/地址）
//   deploy/tianjin/deploy-adapter-host.sh（本机或 SSH 执行，换机只换参数）
// 流水线只通过环境变量点名要开哪些平台；本流水线只开 WorkBuddy。
object DeployTianjinSub2API : BuildType({
    name = "Deploy Tianjin Sub2API"
    description = "构建 Sub2API 镜像并部署到天津，按环境变量启用内置适配器（当前仅 WorkBuddy）。"

    vcs {
        root(DslContext.settingsRoot)
        checkoutMode = CheckoutMode.ON_AGENT
    }

    requirements {
        // 复用 252 agent：它同时能构建镜像并经 cloudflared/FRP 访问天津。
        equals("teamcity.agent.name", "ip_172.19.0.1")
    }

    params {
        // 天津主机必须走 FRP 别名 `tianjin-frp`（252 frps 把天津 22 发布到本机
        // 28083，实测 ~6MB/s）。`tianjin-media` 是 cloudflared 隧道别名，握手
        // 经常超时且只有 ~20KB/s，传 50MB 镜像要十几分钟，不能用于本流水线。
        param("env.TIANJIN_SSH_HOST", "tianjin-frp")
        param("env.TIANJIN_DEPLOY_DIR", "/opt/reeve/stacks/sub2api")
        param("env.TIANJIN_STACK_COMPOSE", "docker-compose.reeve.yml")
        param("env.IMAGE_REPOSITORY", "zjarlin/sub2api")
        // 适配器镜像在天津本机构建（源码几百 KB，走 SSH 即可）。
        param("env.SKIP_BUILD", "0")
        // 本流水线只开 WorkBuddy；其他平台保持目标机 .env 的设置。
        param("env.SUB2API_WORKBUDDY", "1")
    }

    maxRunningBuilds = 1

    steps {
        step {
            name = "Build image, sync and deploy Tianjin Sub2API"
            type = "simpleRunner"
            param("use.custom.script", "true")
            param("script.content", """
                set -euo pipefail

                CHECKOUT="%teamcity.build.checkoutDir%"
                SHA="%build.vcs.number%"
                SHORT_SHA="${'$'}{SHA:0:12}"
                IMAGE="%env.IMAGE_REPOSITORY%:${'$'}SHORT_SHA"
                REMOTE="%env.TIANJIN_SSH_HOST%"
                RDIR="%env.TIANJIN_DEPLOY_DIR%"

                cd "${'$'}CHECKOUT"
                test -x deploy/tianjin/deploy-adapter-host.sh
                test -f deploy/docker-compose.adapters.yml
                test -f deploy/cluster/adapters.env.sh

                echo "##teamcity[progressStart '构建不可变镜像']"
                docker build \
                  --build-arg VERSION="${'$'}SHORT_SHA" \
                  --build-arg COMMIT="${'$'}SHA" \
                  --build-arg DATE="${'$'}(date -u +%%Y-%%m-%%dT%%H:%%M:%%SZ)" \
                  -t "${'$'}IMAGE" .
                echo "##teamcity[progressFinish '构建不可变镜像']"

                echo "##teamcity[progressStart '推送镜像到天津']"
                # 天津与 252 私网不通、也没有共享 registry：经 SSH 直传镜像。
                # 走 FRP 别名（见 TIANJIN_SSH_HOST 注释），50MB 镜像约 10 秒。
                ssh -o BatchMode=yes "${'$'}REMOTE" "hostname" >/dev/null
                docker save "${'$'}IMAGE" | gzip -1 | ssh -o BatchMode=yes "${'$'}REMOTE" "gunzip | docker load"
                echo "##teamcity[progressFinish '推送镜像到天津']"

                echo "##teamcity[progressStart '同步部署文件']"
                ssh -o BatchMode=yes "${'$'}REMOTE" "mkdir -p '${'$'}RDIR'"
                # 只同步部署所需：deploy 定义与 tools 适配器源码。
                # 排除运行期数据目录，避免覆盖目标机的库/缓存。
                tar -cz \
                  --exclude='deploy/data' \
                  --exclude='deploy/postgres_data' \
                  --exclude='deploy/redis_data' \
                  deploy | ssh -o BatchMode=yes "${'$'}REMOTE" "tar -xz -C '${'$'}RDIR'"
                git archive "${'$'}SHA" tools | ssh -o BatchMode=yes "${'$'}REMOTE" "tar -x -C '${'$'}RDIR'"
                ssh -o BatchMode=yes "${'$'}REMOTE" "chmod +x '${'$'}RDIR/deploy/tianjin/deploy-adapter-host.sh' '${'$'}RDIR/deploy/tianjin/deploy-adapter-host-remote.sh' '${'$'}RDIR/deploy/cluster/adapters.env.sh'"
                echo "##teamcity[progressFinish '同步部署文件']"

                echo "##teamcity[progressStart '在天津部署']"
                # 直接在本机执行执行体（流水线已通过 SSH 进入天津）。
                ssh -o BatchMode=yes "${'$'}REMOTE" \
                  "TARGET_DEPLOY_DIR='${'$'}RDIR' \
                   STACK_COMPOSE='%env.TIANJIN_STACK_COMPOSE%' \
                   SUB2API_IMAGE='${'$'}IMAGE' \
                   SKIP_BUILD='%env.SKIP_BUILD%' \
                   SUB2API_WORKBUDDY='%env.SUB2API_WORKBUDDY%' \
                   bash -s" < deploy/tianjin/deploy-adapter-host-remote.sh
                echo "##teamcity[progressFinish '在天津部署']"

                echo "##teamcity[progressStart '验证天津入口']"
                ssh -o BatchMode=yes "${'$'}REMOTE" "docker inspect sub2api --format '{{.State.Health.Status}}' | grep -qx healthy"
                ssh -o BatchMode=yes "${'$'}REMOTE" "curl --fail --silent --show-error --max-time 20 http://127.0.0.1:18080/health"
                ssh -o BatchMode=yes "${'$'}REMOTE" "docker ps --filter name=sub2api --format '{{.Names}} | {{.Image}} | {{.Status}}'"
                echo "##teamcity[progressFinish '验证天津入口']"
                echo "DEPLOYED tianjin sub2api=${'$'}IMAGE"
            """.trimIndent())
        }
    }
})
