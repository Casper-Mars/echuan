import { Link, Outlet } from 'react-router-dom'
import { AutoRefreshSwitch } from './AutoRefreshSwitch'
import { Sidebar } from './Sidebar'

export function Layout() {
  return (
    <div className="app">
      <header className="topbar">
        <Link className="brand" to="/products">
          <span className="brand-mark">驿</span>
          <span className="brand-text">
            驿传 <em>echuan</em>
          </span>
        </Link>
        <div className="topbar-end">
          <AutoRefreshSwitch />
        </div>
      </header>
      <div className="shell">
        <Sidebar />
        <main className="content">
          <Outlet />
        </main>
      </div>
    </div>
  )
}
