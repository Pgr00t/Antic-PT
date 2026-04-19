// Antic-PT v0.2 — AnticipationResolver SDK public exports.

export {
  AnticipationResolver,
} from './resolver';

export type {
  ResolverMeta,
  ResolverOptions,
  ResolverStatus,
  AbandonMeta,
  PatchOp,
  VolatilityLevel,
} from './resolver';

// Default export for script-tag / IIFE usage.
import { AnticipationResolver } from './resolver';
export default AnticipationResolver;
