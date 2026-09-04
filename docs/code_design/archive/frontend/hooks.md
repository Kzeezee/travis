# React Query Hook Patterns

## File location

One hook per resource: `src/hooks/use-tasks.ts`, `src/hooks/use-projects.ts`, etc.

## Structure

A hook bundles queries and mutations for one resource into a single return object. Consumers get data and actions without knowing about query keys, cache invalidation, or API endpoints.

```typescript
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useApi } from "@/lib/api";
import { queryKeys } from "@/lib/query-keys";
import type { ProjectListItem, CreateProjectRequest } from "@/types/project";

export function useProjects() {
  const api = useApi();
  const queryClient = useQueryClient();

  const query = useQuery({
    queryKey: queryKeys.projects(),
    queryFn: () => api.get<ProjectListItem[]>("/projects"),
  });

  const createProject = useMutation({
    mutationFn: (data: CreateProjectRequest) =>
      api.post<ProjectListItem>("/projects", data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.projects() });
    },
  });

  const deleteProject = useMutation({
    mutationFn: (id: number) => api.delete(`/projects/${id}`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.projects() });
    },
  });

  return {
    projects: query.data,
    isLoading: query.isLoading,
    error: query.error,
    createProject,
    deleteProject,
  };
}
```

## Query key factories

Never use raw strings as query keys. Use a centralized factory so typos are caught at compile time and renames are one edit.

```typescript
// src/lib/query-keys.ts
export const queryKeys = {
  projects: () => ["projects"] as const,
  project: (id: number) => ["project", id] as const,
  tasks: () => ["tasks"] as const,
  task: (id: number) => ["task", id] as const,
  activities: (filters?: Record<string, unknown>) =>
    ["activities", filters] as const,
};

// For prefix-based invalidation (wipe all activity queries regardless of filters)
export const queryKeyPrefix = {
  activities: ["activities"],
};
```

## Mutation invalidation

Every mutation must invalidate the queries it affects. This is how the UI stays in sync with the server after writes.

```typescript
// Creating a project adds to the list - invalidate the list query
onSuccess: () => {
  queryClient.invalidateQueries({ queryKey: queryKeys.projects() });
};

// Updating a specific project - invalidate both the detail and list queries
onSuccess: () => {
  queryClient.invalidateQueries({ queryKey: queryKeys.project(id) });
  queryClient.invalidateQueries({ queryKey: queryKeys.projects() });
};

// When filters are involved, invalidate by prefix to catch all filter variants
onSuccess: () => {
  queryClient.invalidateQueries({ queryKey: queryKeyPrefix.activities });
};
```

## Conditional polling

Use `refetchInterval` when you need to poll for background task completion. Always make it conditional so it stops when done.

```typescript
const query = useQuery({
  queryKey: queryKeys.task(id),
  queryFn: () => api.get<TaskDetailResponse>(`/tasks/${id}`),
  refetchInterval: (query) => {
    const status = query.state.data?.execution_status;
    // Poll every 3s while the task is in flight
    if (status === "queued" || status === "running") return 3000;
    // Stop polling once it resolves
    return false;
  },
});
```

## Detail hooks

For detail pages that take an ID param, make the query conditional on the ID being present:

```typescript
export function useProject(id: number | null) {
  const api = useApi();

  return useQuery({
    queryKey: queryKeys.project(id!),
    queryFn: () => api.get<ProjectDetailResponse>(`/projects/${id}`),
    enabled: id !== null,
  });
}
```

## What doesn't belong in a hook

- Business logic (filtering, sorting, transformation of data for display)
- UI state (which modal is open, selected row)
- Side effects beyond cache invalidation (navigation, toasts)

Toasts on mutation error are an exception - some projects handle them in the hook's `onError`, others in the component. Match the project's convention.
