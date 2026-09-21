import jetbrains.buildServer.configs.kotlin.*

version = "2025.11"

project {
    description = "Sub2API 252 集群部署：构建不可变镜像，双副本切换，保留回滚记录并验证 18080；同时构建并上线边缘计算视觉服务 /vision。"
    buildType(Deploy252Cluster)
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
                echo "##teamcity[progressFinish '同步部署文件']"

                echo "##teamcity[progressStart '部署双副本']"
                cd "${'$'}DEPLOY_DIR"
                SUB2API_IMAGE="${'$'}IMAGE" \
                  DEPLOY_DIR="${'$'}DEPLOY_DIR" \
                  CANARY_REPLICAS="%env.CANARY_REPLICAS%" \
                  SUB2API_REPLICAS="%env.SUB2API_REPLICAS%" \
                  "${'$'}DEPLOY_DIR/deploy/cluster/deploy-252.sh"
                echo "##teamcity[progressFinish '部署双副本']"

                echo "##teamcity[progressStart '部署边缘视觉服务']"
                EDGE_VISION_IMAGE="%env.EDGE_VISION_IMAGE_REPOSITORY%:${'$'}SHORT_SHA" \
                  DEPLOY_DIR="${'$'}DEPLOY_DIR" \
                  "${'$'}DEPLOY_DIR/deploy/cluster/deploy-edge-vision.sh"
                echo "##teamcity[progressFinish '部署边缘视觉服务']"

                echo "##teamcity[progressStart '验证 252 入口']"
                curl --fail --silent --show-error --max-time 20 http://127.0.0.1:18080/health >/dev/null
                curl --fail --silent --show-error --max-time 20 http://127.0.0.1:18080/vision/health >/dev/null
                test "${'$'}(docker ps --filter name=sub2api-gateway --filter status=running -q | wc -l | tr -d ' ')" = "1"
                test "${'$'}(docker ps --filter name=edge-vision --filter health=healthy -q | wc -l | tr -d ' ')" = "1"
                test "${'$'}(docker ps --filter label=com.docker.compose.service=sub2api --filter status=running -q | wc -l | tr -d ' ')" -ge "%env.SUB2API_REPLICAS%"
                echo "##teamcity[progressFinish '验证 252 入口']"
                echo "DEPLOYED ${'$'}IMAGE EDGE_VISION=%env.EDGE_VISION_IMAGE_REPOSITORY%:${'$'}SHORT_SHA"
            """.trimIndent())
        }
    }
})
