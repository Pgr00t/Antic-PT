/**
 * Reconciler provides logic for applying RFC 6902 JSON Patch operations.
 * This is a minimal, zero-dependency implementation focused on 'add', 'replace', and 'remove'.
 */

export interface PatchOp {
  op: 'add' | 'remove' | 'replace';
  path: string;
  value?: any;
}

export function applyPatch(data: any, ops: PatchOp[]): any {
  // Use a deep copy to avoid mutating the original object if desired.
  // In Antic-PT context, we usually want to return a fresh object for UI reactivity.
  const result = JSON.parse(JSON.stringify(data));

  for (const op of ops) {
    applyOperation(result, op);
  }

  return result;
}

function applyOperation(obj: any, op: PatchOp) {
  const parts = op.path.split('/').filter(p => p !== '');
  if (parts.length === 0) return;

  let current = obj;
  for (let i = 0; i < parts.length - 1; i++) {
    const key = parts[i];
    if (!(key in current)) {
      if (op.op === 'add') {
        current[key] = {};
      } else {
        return; // Path not found
      }
    }
    current = current[key];
  }

  const targetKey = parts[parts.length - 1];

  switch (op.op) {
    case 'add':
    case 'replace':
      current[targetKey] = op.value;
      break;
    case 'remove':
      if (Array.isArray(current)) {
        const index = parseInt(targetKey, 10);
        if (!isNaN(index)) current.splice(index, 1);
      } else {
        delete current[targetKey];
      }
      break;
  }
}
