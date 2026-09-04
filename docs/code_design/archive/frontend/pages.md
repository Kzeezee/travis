# Page Component Patterns

## File location

One file per route: `src/pages/tasks.tsx`, `src/pages/project-detail.tsx`, etc.

## Structure

A page is a composition layer. It wires data hooks to UI components and manages the UI state (which panel is open, which tab is active). It doesn't contain business logic or complex rendering.

```typescript
import { useState } from "react";
import { useTasks } from "@/hooks/use-tasks";
import { DataTable } from "@/components/data-table/data-table";
import { columns } from "./task-columns"; // or define inline
import { TaskCreateDialog } from "@/components/tasks/task-create-dialog";
import { Button } from "@/components/ui/button";
import { Plus } from "lucide-react";

export default function TasksPage() {
  const { tasks, isLoading } = useTasks();
  const [createOpen, setCreateOpen] = useState(false);

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Tasks</h1>
        <Button onClick={() => setCreateOpen(true)}>
          <Plus className="mr-2 h-4 w-4" />
          New Task
        </Button>
      </div>

      <DataTable
        columns={columns}
        data={tasks ?? []}
        isLoading={isLoading}
        initialSorting={[{ id: "created_at", desc: true }]}
      />

      <TaskCreateDialog
        open={createOpen}
        onOpenChange={setCreateOpen}
      />
    </div>
  );
}
```

## Local state vs server state

**Server state** comes from the hook. Never copy it into `useState`.

```typescript
// Wrong - duplicates server state into local state
const { projects } = useProjects();
const [data, setData] = useState(projects); // stale the moment projects updates

// Right - use the hook data directly
const { projects, isLoading } = useProjects();
// Use projects directly in the JSX
```

**Local state** is for UI concerns only:

```typescript
// Which modal is open
const [editingId, setEditingId] = useState<number | null>(null);

// Which tab is active
const [activeTab, setActiveTab] = useState<"list" | "create">("list");

// Filter/search input (before it's submitted to the server)
const [searchTerm, setSearchTerm] = useState("");
```

## Modal and panel pattern

Modals and side panels are controlled from the page via a nullable ID. The page passes the ID and an `onClose` callback. The modal/panel handles its own data fetching using the ID.

```typescript
const [inspectedId, setInspectedId] = useState<number | null>(null);

// In column actions:
<Button variant="ghost" onClick={() => setInspectedId(row.original.id)}>
  Inspect
</Button>

// At the bottom of the page:
<ProjectInspectorPanel
  projectId={inspectedId}
  onClose={() => setInspectedId(null)}
/>
```

## Loading, error, and empty states

Every data-dependent page needs all three:

```typescript
const { projects, isLoading, error } = useProjects();

if (error) {
  return <div className="p-6 text-destructive">Failed to load projects</div>;
}

// DataTable handles loading (skeleton rows) and empty ("No results") internally.
// But if you're not using DataTable, handle them explicitly:

if (isLoading) {
  return <Skeleton className="h-64 w-full" />;
}

if (!projects?.length) {
  return <EmptyState message="No projects yet" />;
}
```

## Tab pattern

Use shadcn Tabs for pages with multiple views:

```typescript
<Tabs defaultValue="list">
  <TabsList>
    <TabsTrigger value="list">All Tasks</TabsTrigger>
    <TabsTrigger value="create">Create</TabsTrigger>
  </TabsList>
  <TabsContent value="list">
    <DataTable ... />
  </TabsContent>
  <TabsContent value="create">
    <TaskCreateForm />
  </TabsContent>
</Tabs>
```

## Layout expectations

Pages render inside a layout's `<Outlet />`. The layout provides the sidebar, header, and main content padding. Pages shouldn't add their own outer padding or sidebar - just the page content.
