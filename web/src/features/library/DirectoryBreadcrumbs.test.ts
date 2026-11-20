import { render, screen } from '@testing-library/vue'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import type { Entry, Page } from '../../app/contracts'
import DirectoryBreadcrumbs from './DirectoryBreadcrumbs.vue'

function entry(id: number, name: string, parent = 0): Entry {
  return {
    id,
    parent,
    name,
    kind: 'directory',
    size_bytes: 0,
    mtime: '2026-11-20T00:00:00.000Z',
    etag: `etag-${id}`,
    version: 1,
    processed: '',
    restore_id: 0,
    task_status: ''
  }
}

function makePage(options: {
  roots?: { id: number; name: string }[]
  breadcrumbs: Entry[]
  directory?: Entry
}): Page {
  const lastCrumb = options.breadcrumbs[options.breadcrumbs.length - 1] ?? entry(1, '')
  return {
    media_roots: options.roots,
    offset: 0,
    limit: 50,
    total: 0,
    items: [],
    generation: '1',
    refreshing: false,
    refreshed: '2026-11-20T00:00:00.000Z',
    directory: options.directory ?? lastCrumb,
    breadcrumbs: options.breadcrumbs
  }
}

describe('DirectoryBreadcrumbs', () => {
  it('renders single-root breadcrumb with 媒体库 and subdirectory links', async () => {
    const user = userEvent.setup()
    const enter = vi.fn()
    render(DirectoryBreadcrumbs, {
      props: {
        page: makePage({
          breadcrumbs: [entry(1, ''), entry(10, '2026', 1), entry(11, '旅行', 10)]
        }),
        onEnter: enter
      }
    })
    expect(screen.getByRole('button', { name: '媒体库' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '旅行' })).toHaveAttribute('aria-current', 'location')
    await user.click(screen.getByRole('button', { name: '2026' }))
    expect(enter).toHaveBeenCalledExactlyOnceWith(10)
  })

  it('renders direct one-click root tabs for up to 5 media roots and returns to root from subdirectories', async () => {
    const user = userEvent.setup()
    const enter = vi.fn()
    const roots = [
      { id: 1, name: 'color1' },
      { id: 2, name: 'color2' }
    ]
    const view = render(DirectoryBreadcrumbs, {
      props: {
        page: makePage({ roots, breadcrumbs: [entry(1, 'color1')] }),
        onEnter: enter
      }
    })
    const color1 = screen.getByRole('button', { name: 'color1' })
    const color2 = screen.getByRole('button', { name: 'color2' })
    expect(color1).toHaveAttribute('aria-pressed', 'true')
    expect(color2).toHaveAttribute('aria-pressed', 'false')

    // Clicking the already-active root while at root does not trigger a redundant navigation.
    await user.click(color1)
    expect(enter).not.toHaveBeenCalled()

    await user.click(color2)
    expect(enter).toHaveBeenLastCalledWith(2)

    // When inside a subdirectory, clicking the active root navigates back to its root.
    enter.mockClear()
    await view.rerender({
      page: makePage({
        roots,
        breadcrumbs: [entry(1, 'color1'), entry(20, '合集', 1)]
      })
    })
    expect(screen.getByRole('button', { name: '合集' })).toHaveAttribute('aria-current', 'location')
    await user.click(screen.getByRole('button', { name: 'color1' }))
    expect(enter).toHaveBeenCalledExactlyOnceWith(1)
  })

  it('uses a keyboard-accessible dropdown when there are more than 5 media roots', async () => {
    const user = userEvent.setup()
    const enter = vi.fn()
    const roots = Array.from({ length: 6 }, (_, i) => ({ id: i + 1, name: `media-${i + 1}` }))
    render(DirectoryBreadcrumbs, {
      props: {
        page: makePage({
          roots,
          breadcrumbs: [entry(1, 'media-1'), entry(50, '子目录', 1)]
        }),
        onEnter: enter
      }
    })
    const combobox = screen.getByRole('combobox', { name: '切换媒体根目录' })
    expect(combobox).toHaveTextContent('media-1')
    expect(combobox).toHaveTextContent('1/6')

    await user.click(combobox)
    expect(screen.getByRole('listbox', { name: '媒体目录列表' })).toBeInTheDocument()
    await user.click(screen.getByRole('option', { name: 'media-4' }))
    expect(enter).toHaveBeenCalledExactlyOnceWith(4)
    expect(screen.queryByRole('listbox')).not.toBeInTheDocument()

    // Re-selecting the active root via keyboard returns from the subdirectory to its root.
    enter.mockClear()
    combobox.focus()
    await user.keyboard('{Enter}{Enter}')
    expect(enter).toHaveBeenCalledExactlyOnceWith(1)

    // Escape closes the menu without navigating.
    enter.mockClear()
    await user.keyboard('{ArrowDown}{Escape}')
    expect(screen.queryByRole('listbox')).not.toBeInTheDocument()
    expect(enter).not.toHaveBeenCalled()
  })
})
