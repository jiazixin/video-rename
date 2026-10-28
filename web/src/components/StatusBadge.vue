<script setup lang="ts">
  const { status } = defineProps<{ status: string }>();
</script>
<template>
  <span class="status-badge" :data-status="status"
    ><slot name="icon"><span class="status-dot" aria-hidden="true" /></slot><slot
  /></span>
</template>

<style scoped>
  .status-badge {
    --status-ink: var(--palette-676e79);
    --status-bg: var(--palette-f3f4f6);
    --status-line: var(--palette-e1e4e8);
    display: inline-flex;
    align-items: center;
    gap: var(--space-6);
    white-space: nowrap;
    font-size: var(--font-size-11);
    line-height: var(--line-height-1-5);
    padding: var(--space-4) var(--space-8);
    border: 1px solid var(--status-line);
    border-radius: var(--radius-5);
    background: var(--status-bg);
    color: var(--status-ink);
  }
  .status-dot {
    width: 5px;
    height: 5px;
    border-radius: var(--radius-round);
    background: currentColor;
    flex: none;
  }
  .status-badge[data-status='queued'],
  .status-badge[data-status='paused'] {
    --status-ink: var(--palette-996314);
    --status-bg: var(--palette-fff8e8);
    --status-line: var(--palette-f2dfac);
  }
  .status-badge[data-status='scanning'] {
    --status-ink: var(--palette-6a52a5);
    --status-bg: var(--palette-f5f0fc);
    --status-line: var(--palette-e2d7f2);
  }
  .status-badge[data-status='running'] {
    --status-ink: var(--palette-2468af);
    --status-bg: var(--palette-edf5ff);
    --status-line: var(--palette-cfe1f6);
  }
  .status-badge[data-status='manual'] {
    --status-ink: var(--palette-9a4674);
    --status-bg: var(--palette-fcf0f6);
    --status-line: var(--palette-edcddd);
  }
  .status-badge[data-status='automatic'],
  .status-badge[data-status='completed'],
  .status-badge[data-status='done'] {
    --status-ink: var(--tone-green-ink);
    --status-bg: var(--tone-green-bg);
    --status-line: var(--tone-green-line);
  }
  .status-badge[data-status='failed'] {
    --status-ink: var(--chart-failed);
    --status-bg: var(--tone-red-bg);
    --status-line: var(--tone-red-line);
  }
  .status-badge[data-status='partial_failure'],
  .status-badge[data-status='cancelling'] {
    --status-ink: var(--palette-aa5c22);
    --status-bg: var(--palette-fff3e9);
    --status-line: var(--palette-f1d5be);
  }
  .status-badge[data-status='cancelled'],
  .status-badge[data-status='interrupted'] {
    --status-ink: var(--palette-7a6f81);
    --status-bg: var(--palette-f4f1f5);
    --status-line: var(--palette-e3dce7);
  }
  .status-badge[data-status='marked'] {
    --status-ink: var(--palette-497168);
    --status-bg: var(--palette-eef6f3);
    --status-line: var(--palette-d2e6df);
  }

  .status-badge[data-status='skipped'] {
    --status-ink: var(--tone-amber-ink);
    --status-bg: var(--tone-amber-bg);
    --status-line: var(--tone-amber-line);
  }
</style>
