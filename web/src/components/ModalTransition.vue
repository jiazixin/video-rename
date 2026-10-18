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
