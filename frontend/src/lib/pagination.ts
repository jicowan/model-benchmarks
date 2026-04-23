// Shared pagination constants + types (PRD-36). Every list endpoint that
// paginates returns { rows, total } so the UI can render a "showing X-Y of Z"
// indicator without a second round-trip.

export const PAGE_SIZE = 25;

export interface Paginated<T> {
  rows: T[];
  total: number;
}
