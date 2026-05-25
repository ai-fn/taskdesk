export const taskKeys = {
  all: ['tasks'] as const,
  list: (filter?: string) => [...taskKeys.all, 'list', filter ?? ''] as const,
  detail: (id: string) => [...taskKeys.all, 'detail', id] as const,
  dependencies: (id: string) => [...taskKeys.all, 'dependencies', id] as const,
  dependencyTree: (id: string) => [...taskKeys.all, 'dependency-tree', id] as const,
}

export const listKeys = {
  all:  ['lists'] as const,
  list: () => [...listKeys.all, 'list'] as const,
}

export const groupKeys = {
  all:  ['groups'] as const,
  list: () => [...groupKeys.all, 'list'] as const,
}

export const planKeys = {
  latest: () => ['plan', 'latest'] as const,
}
