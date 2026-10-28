<!-- Teleports a dialog to body and animates it in and out. Wrap the v-if'd Modal here: a
     Transition placed inside Modal itself never sees it unmount. -->
<script setup lang="ts">
  // A closing dialog stays in the DOM until it fades out; keep it from taking input or focus meanwhile.
  function retire(el: Element) {
    el.setAttribute('aria-hidden', 'true');
    (el as HTMLElement).inert = true;
  }
</script>
<template>
  <Teleport to="body"
    ><Transition name="dialog" @before-leave="retire"><slot /></Transition
  ></Teleport>
</template>

<style scoped>
  /* Transition classes target the teleported slot root, which has no wrapper scope. */

  :global(.dialog-enter-active) {
    transition: opacity var(--motion-enter) linear;
  }
  :global(.dialog-leave-active) {
    transition: opacity var(--motion-exit) linear;
    pointer-events: none;
  }
  :global(.dialog-enter-from),
  :global(.dialog-leave-to) {
    opacity: 0;
  }
  :global(.dialog-enter-active > .modal) {
    transition: transform var(--motion-enter) linear;
  }
  :global(.dialog-leave-active > .modal) {
    transition: transform var(--motion-exit) linear;
  }
  :global(.dialog-enter-from > .modal) {
    transform: translateY(8px) scale(0.98);
  }
  :global(.dialog-leave-to > .modal) {
    transform: scale(0.98);
  }
</style>
