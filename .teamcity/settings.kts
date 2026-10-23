import jetbrains.buildServer.configs.kotlin.*

version = "2025.11"

project {
    description = "构建并启动曼波 GPT-SoVITS、视频配音流水线与 edge-media 编排，发布内网端口供网关的 /media/* 调用。"
    buildType(Deploy182Gateway)
    buildType(DeployTianjinMedia)
    buildType(DeployTianjinSub2API)
}

object Deploy182Gateway : BuildType({
    name = "Deploy 182 Gateway"
    description = "在 182 构建镜像并就地重建 Sub2API 单容器（18080 由该容器直接发布），健康检查失败自动回滚上一版镜像。"

    vcs {
        root(DslContext.settingsRoot)
        checkoutMode = CheckoutMode.ON_AGENT
    }

    requirements {
        equals("teamcity.agent.name", "ip_172.19.0.1")
    }

    params {
        // 主机地址的唯一来源：其他脚本/文档只引用变量，不再各自写死 IP。
        param("env.APP_HOST", "192.168.31.182")
        // 182 的线上目录：compose、数据、部署标记统一收敛在 /root/DockerCompose/<服务>/。
        param("env.DEPLOY_DIR", "/root/DockerCompose/sub2api")
        param("env.SUB2API_CONTAINER", "sub2api")
        // 由流水线负责构建镜像；本地手动复跑时可置 0 只做部署。
        param("env.SUB2API_BUILD", "1")
        param("env.SUB2API_GIT_SHA", "%build.vcs.number%")
        param("env.SUB2API_PORT", "18080")
        param("env.IMAGE_REPOSITORY", "zjarlin/sub2api")
    }

    maxRunningBuilds = 1

    triggers {
        trigger {
            type = "vcsTrigger"
            param("branchFilter", "+:<default>")
            // 只改 TeamCity 配置本身（.teamcity/**）时不重跑部署，
            // 避免因为改流水线文案把线上网关/天津服务重启一遍。
            param("triggerRules", "+:.:-:.teamcity/**")
        }
    }

    steps {
        step {
            name = "Build and deploy gateway on 182"
            type = "simpleRunner"
            // TeamCity 用 /bin/sh 执行脚本文件，182 上 /bin/sh 是 dash（不支持 pipefail），
            // 所以这里只做一件事：显式切到 bash 并调用仓库内版本化的部署脚本。
            // 逻辑放在 deploy/gateway/deploy-gateway.sh 里，可 bash -n 静态校验、可本地复跑。
            param("use.custom.script", "true")
            param("script.content", """
                # 部署要动 docker 与 /root/DockerCompose，agent 以 teamcity-agent 运行，
                # 因此只经 sudo 走一个固定入口脚本（不接受任意路径/任意命令）。
                # 注意：sudo 默认 env_reset，不能靠环境变量传主机/目录，必须显式命令行参数。
                # 镜像标签的截断由脚本按 --git-sha 处理，避免依赖 TeamCity 参数表达式语法。
                sudo -n /usr/local/sbin/sub2api-deploy-182 \
                  --repo "%teamcity.build.checkoutDir%" \
                  --image "%env.IMAGE_REPOSITORY%:%env.SUB2API_GIT_SHA%" \
                  --image-repository "%env.IMAGE_REPOSITORY%" \
                  --app-host "%env.APP_HOST%" \
                  --deploy-dir "%env.DEPLOY_DIR%" \
                  --container "%env.SUB2API_CONTAINER%" \
                  --port "%env.SUB2API_PORT%"
            """.trimIndent())
        }
    }
})

// 天津海光 DCU 机器：曼波 TTS + 视频配音 + edge-media 编排。
//
// 镜像在天津本机构建：海光 DTK 基础镜像与曼波权重体积很大，跨公网搬运不现实，
// 而源码只有几百 KB，所以这里把源码同步过去后在天津直接 docker build。
//
// 182 与天津私网不互通，走 EasyTier 组网：182 的 et-edge(10.144.144.1) 与天津的
// et-tianjin-ai(10.144.144.97, --proxy-networks=192.168.9.0/24) 组成同一张网，
// 因此本机可直接 ssh 天津的 192.168.9.x。SSH 别名 `tianjin-ai` 由 ~/.ssh/config 定义；
// 旧的 FRP 通道（tianjin-frp / 已退役主机的 frps:28083）已随该主机退役，不要再用。
object DeployTianjinMedia : BuildType({
    name = "Deploy Media (GPU)"
    description = "构建并启动曼波 GPT-SoVITS、视频配音流水线与 edge-media 编排，发布内网端口供网关的 /media/* 调用。"

    vcs {
        root(DslContext.settingsRoot)
        checkoutMode = CheckoutMode.ON_AGENT
    }

    requirements {
        // 固定用 182 上的 root agent：它有 docker、EasyTier 路由与天津的 SSH 私钥。
        equals("teamcity.agent.name", "ip_172.19.0.1")
    }

    params {
        // EasyTier 上的天津 GPU 机器（192.168.9.97）root 别名。
        param("env.TIANJIN_SSH_HOST", "tianjin-ai")
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
        // 网络视频生成（Seedance 2.0 等）由 182 网关侧账号池承接，天津默认关闭。
        param("env.MEDIA_VIDEO_GENERATION_ENABLED", "0")
        param("env.MEDIA_VIDEO_GENERATION_UPSTREAM_URL", "")
    }

    maxRunningBuilds = 1

    triggers {
        trigger {
            type = "vcsTrigger"
            param("branchFilter", "+:<default>")
            // 只改 TeamCity 配置本身（.teamcity/**）时不重跑部署，
            // 避免因为改流水线文案把线上网关/天津服务重启一遍。
            param("triggerRules", "+:.:-:.teamcity/**")
        }
    }

    steps {
        step {
            name = "Build and deploy media services"
            type = "simpleRunner"
            param("use.custom.script", "true")
            param("script.content", """
                # TeamCity 用 /bin/sh 执行本文件（182 上是 dash，不支持 pipefail）。
                # 用 BASH_VERSION 判断是否已由 bash 重新执行，避免无限递归。
                [ -n "${'$'}{BASH_VERSION:-}" ] || exec bash -eo pipefail "${'$'}0" "${'$'}@"

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
        // 固定用 182 上的 root agent：它能构建镜像并经 EasyTier 访问天津。
        equals("teamcity.agent.name", "ip_172.19.0.1")
    }

    params {
        // 天津主机走 EasyTier 别名 `tianjin-ai`（直连 192.168.9.97:22）。
        // 旧 FRP 通道已随该主机退役；cloudflared 握手不稳，都不再使用。
        param("env.TIANJIN_SSH_HOST", "tianjin-ai")
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
                # TeamCity 用 /bin/sh 执行本文件（182 上是 dash，不支持 pipefail）。
                # 用 BASH_VERSION 判断是否已由 bash 重新执行，避免无限递归。
                [ -n "${'$'}{BASH_VERSION:-}" ] || exec bash -eo pipefail "${'$'}0" "${'$'}@"

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
                # 天津与本机没有共享 registry：经 EasyTier 的 SSH 直传镜像。
                # 走 tianjin-ai 别名（见 TIANJIN_SSH_HOST 注释）。
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
