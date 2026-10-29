import { createContext, useContext, type ReactNode } from 'react'

export interface DrawerContent {
  title: string
  body: ReactNode
}

/** 页面通过该 context 打开右侧详情抽屉（本任务只提供容器与占位内容）。 */
export const DrawerContext = createContext<(content: DrawerContent) => void>(() => {})
export const DrawerTitleContext = createContext<(title: string) => void>(() => {})

export function useDrawer(): (content: DrawerContent) => void {
  return useContext(DrawerContext)
}
