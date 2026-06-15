<template>
  <div ref="containerRef" class="home-gateway-scene" :class="{ 'is-fallback': !webglReady }" aria-hidden="true">
    <div class="home-gateway-scene__fallback">
      <span v-for="index in 18" :key="index" class="home-gateway-scene__fallback-node"></span>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import * as THREE from 'three'

const containerRef = ref<HTMLElement | null>(null)
const webglReady = ref(false)

let renderer: THREE.WebGLRenderer | null = null
let scene: THREE.Scene | null = null
let camera: THREE.PerspectiveCamera | null = null
let animationFrame = 0
let resizeObserver: ResizeObserver | null = null
let prefersReducedMotion: MediaQueryList | null = null
let startedAt = 0
const pointer = new THREE.Vector2(0, 0)
const targetPointer = new THREE.Vector2(0, 0)
const packetObjects: THREE.Object3D[] = []
const pulseObjects: THREE.Object3D[] = []

function canUseWebGL(): boolean {
  try {
    const canvas = document.createElement('canvas')
    return !!(window.WebGLRenderingContext && (canvas.getContext('webgl') || canvas.getContext('experimental-webgl')))
  } catch {
    return false
  }
}

function makeLine(points: THREE.Vector3[], color: number, opacity = 0.72): THREE.Line {
  const geometry = new THREE.BufferGeometry().setFromPoints(points)
  const material = new THREE.LineBasicMaterial({
    color,
    transparent: true,
    opacity
  })
  return new THREE.Line(geometry, material)
}

function buildScene(width: number, height: number): void {
  scene = new THREE.Scene()
  scene.fog = new THREE.FogExp2(0xfff3bf, 0.032)

  camera = new THREE.PerspectiveCamera(48, width / height, 0.1, 120)
  camera.position.set(0, 8.5, 22)
  camera.lookAt(0, 0, 0)

  const ambient = new THREE.AmbientLight(0xffffff, 2.1)
  scene.add(ambient)

  const keyLight = new THREE.DirectionalLight(0xffffff, 2.4)
  keyLight.position.set(-8, 10, 8)
  scene.add(keyLight)

  const rimLight = new THREE.PointLight(0xff5fa2, 80, 46)
  rimLight.position.set(8, 5, 6)
  scene.add(rimLight)

  const grid = new THREE.GridHelper(56, 28, 0x000000, 0x555555)
  grid.position.y = -2.4
  const gridMaterial = grid.material as THREE.Material
  gridMaterial.transparent = true
  gridMaterial.opacity = 0.24
  scene.add(grid)

  const coreGroup = new THREE.Group()
  coreGroup.name = 'gateway-core'

  const ringColors = [0xffdc58, 0xff5fa2, 0x35d9ff, 0x8fff6a, 0x7084ff]

  for (let index = 0; index < 5; index += 1) {
    const ring = new THREE.Mesh(
      new THREE.TorusGeometry(2.15 + index * 0.5, 0.055, 10, 160),
      new THREE.MeshBasicMaterial({
        color: ringColors[index],
        transparent: true,
        opacity: 0.92,
        side: THREE.DoubleSide
      })
    )
    ring.rotation.x = Math.PI / 2
    ring.rotation.z = index * 0.42
    ring.userData.speed = 0.18 + index * 0.035
    coreGroup.add(ring)
    pulseObjects.push(ring)
  }

  const coreGeometry = new THREE.IcosahedronGeometry(1.35, 2)
  const core = new THREE.Mesh(
    coreGeometry,
    new THREE.MeshStandardMaterial({
      color: 0xffdc58,
      emissive: 0xff5fa2,
      emissiveIntensity: 0.12,
      metalness: 0.04,
      roughness: 0.58
    })
  )
  coreGroup.add(core)
  pulseObjects.push(core)

  const coreOutline = new THREE.LineSegments(
    new THREE.EdgesGeometry(coreGeometry),
    new THREE.LineBasicMaterial({ color: 0x000000, transparent: true, opacity: 0.9 })
  )
  coreGroup.add(coreOutline)
  scene.add(coreGroup)

  const nodeGeometry = new THREE.BoxGeometry(0.6, 0.6, 0.6)
  const nodeOutlineGeometry = new THREE.EdgesGeometry(nodeGeometry)
  const nodePositions = [
    [-9.2, 0.8, -5.2],
    [-7.1, 2.8, 3.6],
    [-3.5, -0.1, 7.3],
    [3.8, 2.9, 5.4],
    [7.9, 0.1, -3.6],
    [9.6, 3.1, 2.6],
    [0, 4.8, -7.5]
  ]

  const packetGeometry = new THREE.SphereGeometry(0.14, 12, 12)
  const packetColors = [0x000000, 0xff5fa2, 0x35d9ff, 0x8fff6a]
  const routeColors = [0x000000, 0xff5fa2, 0x236bff, 0x00a5cf, 0x2f9b00]

  for (const [index, position] of nodePositions.entries()) {
    const node = new THREE.Mesh(
      nodeGeometry,
      new THREE.MeshStandardMaterial({
        color: ringColors[index % ringColors.length],
        emissive: ringColors[index % ringColors.length],
        emissiveIntensity: 0.06,
        metalness: 0.02,
        roughness: 0.52
      })
    )
    node.position.set(position[0], position[1], position[2])
    node.rotation.set(0.5, index * 0.7, 0.2)
    node.userData.floatOffset = index * 0.8
    scene.add(node)
    pulseObjects.push(node)

    const nodeOutline = new THREE.LineSegments(
      nodeOutlineGeometry,
      new THREE.LineBasicMaterial({ color: 0x000000, transparent: true, opacity: 0.86 })
    )
    nodeOutline.position.copy(node.position)
    nodeOutline.rotation.copy(node.rotation)
    nodeOutline.userData.floatOffset = node.userData.floatOffset
    scene.add(nodeOutline)
    pulseObjects.push(nodeOutline)

    const start = node.position.clone()
    const end = new THREE.Vector3(0, 0, 0)
    const middle = start.clone().multiplyScalar(0.46)
    middle.y += 1.2 + (index % 3) * 0.4
    const route = makeLine([start, middle, end], routeColors[index % routeColors.length], 0.68)
    scene.add(route)

    const packet = new THREE.Mesh(
      packetGeometry,
      new THREE.MeshBasicMaterial({
        color: packetColors[index % packetColors.length],
        transparent: true,
        opacity: 0.95
      })
    )
    packet.userData.start = start
    packet.userData.middle = middle
    packet.userData.end = end
    packet.userData.phase = index / nodePositions.length
    packet.userData.speed = 0.17 + (index % 4) * 0.035
    scene.add(packet)
    packetObjects.push(packet)
  }

  const particleCount = window.innerWidth < 768 ? 50 : 120
  const particlePositions = new Float32Array(particleCount * 3)
  for (let index = 0; index < particleCount; index += 1) {
    particlePositions[index * 3] = (Math.random() - 0.5) * 42
    particlePositions[index * 3 + 1] = Math.random() * 14 - 3
    particlePositions[index * 3 + 2] = (Math.random() - 0.5) * 34
  }
  const particleGeometry = new THREE.BufferGeometry()
  particleGeometry.setAttribute('position', new THREE.BufferAttribute(particlePositions, 3))
  const particles = new THREE.Points(
    particleGeometry,
    new THREE.PointsMaterial({
      color: 0x000000,
      size: 0.055,
      transparent: true,
      opacity: 0.22
    })
  )
  particles.name = 'field-particles'
  scene.add(particles)
}

function bezier(start: THREE.Vector3, middle: THREE.Vector3, end: THREE.Vector3, t: number): THREE.Vector3 {
  const a = start.clone().lerp(middle, t)
  const b = middle.clone().lerp(end, t)
  return a.lerp(b, t)
}

function renderFrame(): void {
  if (!renderer || !scene || !camera) return

  const elapsed = (performance.now() - startedAt) / 1000
  pointer.lerp(targetPointer, 0.05)

  scene.rotation.y = pointer.x * 0.08
  scene.rotation.x = -pointer.y * 0.035

  for (const object of pulseObjects) {
    object.rotation.y += object.userData.speed ?? 0.008
    object.rotation.z += (object.userData.speed ?? 0.004) * 0.25
    const pulse = 1 + Math.sin(elapsed * 2.1 + (object.userData.floatOffset ?? 0)) * 0.035
    object.scale.setScalar(pulse)
  }

  for (const packet of packetObjects) {
    const progress = (elapsed * packet.userData.speed + packet.userData.phase) % 1
    packet.position.copy(bezier(packet.userData.start, packet.userData.middle, packet.userData.end, progress))
    packet.scale.setScalar(0.72 + Math.sin(progress * Math.PI) * 1.25)
  }

  const particles = scene.getObjectByName('field-particles')
  if (particles) {
    particles.rotation.y = elapsed * 0.018
  }

  renderer.render(scene, camera)
  if (!prefersReducedMotion?.matches) {
    animationFrame = requestAnimationFrame(renderFrame)
  }
}

function resize(): void {
  const container = containerRef.value
  if (!container || !renderer || !camera) return
  const width = Math.max(container.clientWidth, 1)
  const height = Math.max(container.clientHeight, 1)
  renderer.setSize(width, height, false)
  renderer.setPixelRatio(Math.min(window.devicePixelRatio || 1, 1.7))
  camera.aspect = width / height
  camera.updateProjectionMatrix()
}

function onPointerMove(event: PointerEvent): void {
  const container = containerRef.value
  if (!container) return
  const rect = container.getBoundingClientRect()
  targetPointer.x = ((event.clientX - rect.left) / rect.width - 0.5) * 2
  targetPointer.y = ((event.clientY - rect.top) / rect.height - 0.5) * 2
}

function dispose(): void {
  cancelAnimationFrame(animationFrame)
  resizeObserver?.disconnect()
  containerRef.value?.removeEventListener('pointermove', onPointerMove)
  if (scene) {
    scene.traverse((object: THREE.Object3D) => {
      const mesh = object as THREE.Mesh
      if (mesh.geometry) mesh.geometry.dispose()
      const material = mesh.material
      if (Array.isArray(material)) {
        material.forEach((item) => item.dispose())
      } else if (material) {
        material.dispose()
      }
    })
  }
  renderer?.dispose()
  renderer?.domElement.remove()
  renderer = null
  scene = null
  camera = null
}

onMounted(() => {
  const container = containerRef.value
  if (!container || !canUseWebGL()) return

  const width = Math.max(container.clientWidth, 1)
  const height = Math.max(container.clientHeight, 1)

  buildScene(width, height)
  renderer = new THREE.WebGLRenderer({ antialias: true, alpha: true, powerPreference: 'high-performance' })
  renderer.outputColorSpace = THREE.SRGBColorSpace
  renderer.setClearColor(0x000000, 0)
  renderer.setSize(width, height, false)
  renderer.setPixelRatio(Math.min(window.devicePixelRatio || 1, 1.7))
  container.appendChild(renderer.domElement)

  webglReady.value = true
  startedAt = performance.now()
  prefersReducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)')
  container.addEventListener('pointermove', onPointerMove, { passive: true })
  resizeObserver = new ResizeObserver(resize)
  resizeObserver.observe(container)

  renderFrame()
})

onBeforeUnmount(dispose)
</script>

<style scoped>
.home-gateway-scene {
  position: absolute;
  inset: 0;
  overflow: hidden;
  background:
    radial-gradient(circle at 54% 42%, rgba(255, 220, 88, 0.82), transparent 20rem),
    radial-gradient(circle at 26% 34%, rgba(53, 217, 255, 0.58), transparent 21rem),
    radial-gradient(circle at 74% 68%, rgba(255, 95, 162, 0.56), transparent 19rem),
    linear-gradient(to right, rgba(0, 0, 0, 0.18) 1px, transparent 1px),
    linear-gradient(to bottom, rgba(0, 0, 0, 0.18) 1px, transparent 1px),
    #fff3bf;
  background-size: auto, auto, auto, 70px 70px, 70px 70px, auto;
}

.home-gateway-scene :deep(canvas) {
  display: block;
  width: 100%;
  height: 100%;
}

.home-gateway-scene__fallback {
  position: absolute;
  inset: 0;
  opacity: 0;
  background-image:
    linear-gradient(rgba(0, 0, 0, 0.2) 1px, transparent 1px),
    linear-gradient(90deg, rgba(0, 0, 0, 0.2) 1px, transparent 1px);
  background-size: 70px 70px;
  transition: opacity 0.2s ease;
}

.home-gateway-scene.is-fallback .home-gateway-scene__fallback {
  opacity: 1;
}

.home-gateway-scene__fallback-node {
  position: absolute;
  width: 14px;
  height: 14px;
  border: 2px solid #000;
  border-radius: 3px;
  background: #ffdc58;
  box-shadow: 4px 4px 0 #000;
}

.home-gateway-scene__fallback-node:nth-child(2n) {
  background: #35d9ff;
}

.home-gateway-scene__fallback-node:nth-child(3n) {
  background: #ff5fa2;
}

.home-gateway-scene__fallback-node:nth-child(5n) {
  background: #8fff6a;
}

.home-gateway-scene__fallback-node:nth-child(1) { left: 12%; top: 24%; }
.home-gateway-scene__fallback-node:nth-child(2) { left: 24%; top: 56%; }
.home-gateway-scene__fallback-node:nth-child(3) { left: 36%; top: 18%; }
.home-gateway-scene__fallback-node:nth-child(4) { left: 48%; top: 48%; }
.home-gateway-scene__fallback-node:nth-child(5) { left: 62%; top: 28%; }
.home-gateway-scene__fallback-node:nth-child(6) { left: 78%; top: 42%; }
.home-gateway-scene__fallback-node:nth-child(7) { left: 86%; top: 66%; }
.home-gateway-scene__fallback-node:nth-child(8) { left: 18%; top: 78%; }
.home-gateway-scene__fallback-node:nth-child(9) { left: 68%; top: 76%; }
.home-gateway-scene__fallback-node:nth-child(10) { left: 54%; top: 64%; }
.home-gateway-scene__fallback-node:nth-child(11) { left: 31%; top: 39%; }
.home-gateway-scene__fallback-node:nth-child(12) { left: 72%; top: 13%; }
.home-gateway-scene__fallback-node:nth-child(13) { left: 9%; top: 49%; }
.home-gateway-scene__fallback-node:nth-child(14) { left: 42%; top: 83%; }
.home-gateway-scene__fallback-node:nth-child(15) { left: 91%; top: 24%; }
.home-gateway-scene__fallback-node:nth-child(16) { left: 57%; top: 9%; }
.home-gateway-scene__fallback-node:nth-child(17) { left: 7%; top: 12%; }
.home-gateway-scene__fallback-node:nth-child(18) { left: 83%; top: 83%; }

@media (prefers-reduced-motion: reduce) {
  .home-gateway-scene {
    filter: none;
  }
}
</style>
