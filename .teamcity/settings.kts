import jetbrains.buildServer.configs.kotlin.*

version = "2025.11"

project {
    description = "Sub2API 双端部署：252 承载公网入口与编排（构建不可变镜像、双副本切换并验证 18080），天津海光 DCU 承载曼波 TTS / 视频配音 / 视频生成等重模型服务。"
    buildType(Deploy252Cluster)
    buildType(DeployTianjinMedia)
}

object Deploy252Cluster : BuildType({
    name = "Deploy 252 Cluster"
    description = "在 252 Docker Agent 上构建镜像并部署两个 Sub2API 副本，18080 由稳定 Nginx 入口承载；边缘视觉服务作为内部上游由 /vision 路径反代。"

    vcs {
        root(DslContext.settingsRoot)
        checkoutMode = CheckoutMode.ON_AGENT
    }

    requirements {
        equals("teamcity.agent.name", "ip_172.19.0.1")
    }

    params {
        param("env.DEPLOY_DIR", "/opt/sub2api")
        param("env.CANARY_REPLICAS", "1")
        param("env.SUB2API_REPLICAS", "2")
        param("env.IMAGE_REPOSITORY", "zjarlin/sub2api")
        param("env.EDGE_VISION_IMAGE_REPOSITORY", "zjarlin/edge-vision")
        param("env.EDGE_LAYA_IMAGE_REPOSITORY", "zjarlin/edge-laya")
        param("env.EDGE_MEDIA_IMAGE_REPOSITORY", "zjarlin/edge-media")
        param("env.EDGE_MEDIA_ENABLED", "1")
        // 边缘媒体走网关：GATEWAY_MEDIA_ENABLED=1 让子服务通过 /media/* 暴露。
        param("env.SUB2API_EDGE_MEDIA", "1")
        // 252 只做编排：TTS / 视频配音都转发到天津 GPU 机器。
        // 天津通过 FRP 把 edge-media(28084) 与 gpt-sovits(28085) 暴露到 252 的 frps。
        param("env.MEDIA_TTS_ENABLED", "1")
        // FRP 端口在 252 本机 loopback 上侦听（公网出口不支持 hairpin NAT）。
        param("env.MEDIA_TTS_UPSTREAM_URL", "http://127.0.0.1:28085")
        param("env.MEDIA_DUBBING_ENABLED", "1")
        param("env.MEDIA_DUBBING_COMMAND", "")
        param("env.MEDIA_VIDEO_UPSTREAM_URL", "http://127.0.0.1:28084")
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
                chmod +x "${'$'}DEPLOY_DIR/deploy/cluster/deploy-laya.sh"
                echo "##teamcity[progressFinish '同步部署文件']"

                echo "##teamcity[progressStart '部署双副本']"
                cd "${'$'}DEPLOY_DIR"
                SUB2API_IMAGE="${'$'}IMAGE" \
                  DEPLOY_DIR="${'$'}DEPLOY_DIR" \
                  CANARY_REPLICAS="%env.CANARY_REPLICAS%" \
                  SUB2API_REPLICAS="%env.SUB2API_REPLICAS%" \
                  SUB2API_EDGE_MEDIA="%env.SUB2API_EDGE_MEDIA%" \
                  "${'$'}DEPLOY_DIR/deploy/cluster/deploy-252.sh"
                echo "##teamcity[progressFinish '部署双副本']"

                echo "##teamcity[progressStart '部署边缘视觉服务']"
                EDGE_VISION_IMAGE="%env.EDGE_VISION_IMAGE_REPOSITORY%:${'$'}SHORT_SHA" \
                  DEPLOY_DIR="${'$'}DEPLOY_DIR" \
                  "${'$'}DEPLOY_DIR/deploy/cluster/deploy-edge-vision.sh"
                echo "##teamcity[progressFinish '部署边缘视觉服务']"

                if [ "%env.EDGE_MEDIA_ENABLED%" = "1" ]; then
                  echo "##teamcity[progressStart '部署边缘媒体服务']"
                  EDGE_MEDIA_IMAGE="%env.EDGE_MEDIA_IMAGE_REPOSITORY%:${'$'}SHORT_SHA" \
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

                if [ "%env.EDGE_LAYA_ENABLED%" = "1" ]; then
                  echo "##teamcity[progressStart '部署 Laya 决策模型']"
                  EDGE_LAYA_IMAGE="%env.EDGE_LAYA_IMAGE_REPOSITORY%:${'$'}SHORT_SHA" \
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
                if [ "%env.EDGE_LAYA_ENABLED%" = "1" ]; then
                  docker exec edge-laya curl --fail --silent --show-error --max-time 20 http://127.0.0.1:18082/health >/dev/null
                fi
                test "${'$'}(docker ps --filter name=sub2api-gateway --filter status=running -q | wc -l | tr -d ' ')" = "1"
                test "${'$'}(docker ps --filter name=edge-vision --filter health=healthy -q | wc -l | tr -d ' ')" = "1"
                if [ "%env.EDGE_MEDIA_ENABLED%" = "1" ]; then
                  test "${'$'}(docker ps --filter name=edge-media --filter health=healthy -q | wc -l | tr -d ' ')" = "1"
                fi
                test "${'$'}(docker ps --filter label=com.docker.compose.service=sub2api --filter status=running -q | wc -l | tr -d ' ')" -ge "%env.SUB2API_REPLICAS%"
                echo "##teamcity[progressFinish '验证 252 入口']"
                echo "DEPLOYED ${'$'}IMAGE EDGE_VISION=%env.EDGE_VISION_IMAGE_REPOSITORY%:${'$'}SHORT_SHA EDGE_MEDIA=%env.EDGE_MEDIA_IMAGE_REPOSITORY%:${'$'}SHORT_SHA"
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
    name = "Deploy Tianjin Media (GPU)"
    description = "把源码同步到天津海光 DCU 机器，本机构建并启动曼波 GPT-SoVITS、视频配音流水线与 edge-media 编排，发布内网端口供 252 的 /media/* 调用。"

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
            name = "Sync source and deploy Tianjin media stack over SSH"
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

                echo "##teamcity[progressStart '同步源码到天津']"
                ssh -o BatchMode=yes "${'$'}REMOTE" "mkdir -p '${'$'}RDIR'"
                # 天津只需要 edge-media 与部署脚本；整仓库归档经慢速隧道要传 ~34MB，
                # 只发这两个目录（<0.1MB）即可。
                git archive "${'$'}SHA" edge-media deploy | ssh -o BatchMode=yes "${'$'}REMOTE" "tar -x -C '${'$'}RDIR'"
                ssh -o BatchMode=yes "${'$'}REMOTE" "chmod +x '${'$'}RDIR/deploy/tianjin/deploy-tianjin-media.sh'"
                echo "##teamcity[progressFinish '同步源码到天津']"

                echo "##teamcity[progressStart '部署天津 GPU 媒体三件套']"
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
                echo "##teamcity[progressFinish '部署天津 GPU 媒体三件套']"

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
