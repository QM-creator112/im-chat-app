import { gsap } from 'gsap'

/**
 * 动效工具
 *
 * 设计约束（来自 design-taste-frontend / app-screens §2.8）：
 *   「App 屏没有动效下限。动效是反馈：按压、悬停、焦点、菜单或面板打开、**一行到达**。
 *     屏幕上不做入场动画。」
 *
 * 所以这里只有「一行到达」用 GSAP，其余按压/悬停/焦点反馈交给 CSS ——
 * 用 CSS 能做到的不用 JS 做，减少主线程占用与清理负担。
 *
 * 为什么「行到达」用 GSAP 而不是 CSS animation：
 *   1. 要按 key 找到**刚到达的那一行**，不能整列表重播；
 *   2. 消息连续快速到达时要能被覆盖（overwrite），而不是动画叠加；
 *   3. 结束时用 clearProps 清掉内联样式，避免残留 transform 影响布局。
 */

/** 用户是否要求减少动效（系统级无障碍设置） */
export function prefersReducedMotion(): boolean {
  if (typeof window === 'undefined') return true
  return window.matchMedia?.('(prefers-reduced-motion: reduce)').matches === true
}

/**
 * 新消息「行到达」：从下方 12px 淡入，180ms。
 *
 * 参数用一个元素而不是选择器：调用方已经拿到确切节点，
 * 避免选择器在长列表里重新查询、也避免作用域问题。
 *
 * @param el 刚插入的那一行（找不到就什么都不做）
 */
export function animateRowArrive(el: Element | null | undefined): void {
  if (!el) return
  if (prefersReducedMotion()) return

  gsap.fromTo(
    el,
    { y: 12, autoAlpha: 0 },
    {
      y: 0,
      autoAlpha: 1,
      duration: 0.18,
      ease: 'power2.out',
      overwrite: 'auto',
      // 动画结束清除内联样式：否则 y/opacity 会一直留在 DOM 上，
      // 影响后续布局计算与截图比对
      clearProps: 'transform,opacity,visibility',
    },
  )
}
