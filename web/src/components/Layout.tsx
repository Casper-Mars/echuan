import { useCallback, useEffect, useState } from 'react'
import { Link, Outlet, useLocation } from 'react-router-dom'
import { Breadcrumbs } from './Breadcrumbs'
import { Drawer } from './Drawer'
import { DrawerContext, type DrawerContent } from './DrawerContext'
import { Sidebar } from './Sidebar'

export function Layout() {
  const location = useLocation()
  const [drawer, setDrawer] = useState<DrawerContent | null>(null)

  const openDrawer = useCallback((content: DrawerContent) => setDrawer(content), [])
  const closeDrawer = useCallback(() => setDrawer(null), [])

  // 路由切换时收起抽屉，避免详情内容与页面层级不一致。
  useEffect(() => {
    setDrawer(null)
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
          {drawer?.body}
        </Drawer>
      </div>
    </DrawerContext.Provider>
  )
}
