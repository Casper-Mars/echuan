import { useCallback, useEffect, useState } from 'react'
import { Link, Outlet, useLocation } from 'react-router-dom'
import { rememberMenuPath } from '../lib/menuMemory'
import { Breadcrumbs } from './Breadcrumbs'
import { Drawer } from './Drawer'
import { DrawerContext, DrawerTitleContext, type DrawerContent } from './DrawerContext'
import { Sidebar } from './Sidebar'

export function Layout() {
  const location = useLocation()
  const [drawer, setDrawer] = useState<DrawerContent | null>(null)

  const openDrawer = useCallback((content: DrawerContent) => setDrawer(content), [])
  const closeDrawer = useCallback(() => setDrawer(null), [])
  const updateDrawerTitle = useCallback((title: string) => {
    setDrawer((current) => current && current.title !== title ? { ...current, title } : current)
  }, [])

  // 路由切换时收起抽屉，避免详情内容与页面层级不一致。
  // 同时记录侧边栏入口的最后停留路径，方便从别处一键回到原层级。
  useEffect(() => {
    setDrawer(null)
    rememberMenuPath(location.pathname)
  }, [location.pathname])

  return (
    <DrawerContext.Provider value={openDrawer}>
      <div className="app">
        <header className="topbar">
          <Link className="brand" to="/products">
            <span className="brand-mark">驿</span>
            <span className="brand-text">
              驿传 <em>echuan</em>
            </span>
          </Link>
        </header>
        <div className="shell">
          <Sidebar />
          <main className="content">
            <Breadcrumbs />
            <Outlet />
          </main>
        </div>
        <Drawer open={drawer !== null} title={drawer?.title ?? ''} onClose={closeDrawer}>
          <DrawerTitleContext.Provider value={updateDrawerTitle}>
            {drawer?.body}
          </DrawerTitleContext.Provider>
        </Drawer>
      </div>
    </DrawerContext.Provider>
  )
}
