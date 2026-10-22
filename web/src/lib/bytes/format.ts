export const formatSize = (v: number) =>
  v >= 1073741824
    ? `${(v / 1073741824).toFixed(1)} GB`
    : v >= 1048576
      ? `${(v / 1048576).toFixed(1)} MB`
      : v >= 1024
        ? `${(v / 1024).toFixed(1)} KB`
        : `${v} B`
