import { useEffect, useState } from 'react';

export type Connection = { id: string; databases: Record<string, string[]>; managed: boolean; revision: string };
export type CollectionGrant = { connectionId: string; database: string; collection: string };
export type DatasetField = { name: string; type: string };
export type Dataset = { id: string; connectionId: string; database: string; collection: string; pipelineText: string; fields: DatasetField[]; revision: string };
export type DatasetGrant = { datasetId: string; fields: string[] };
export type Grant = CollectionGrant | DatasetGrant;
export type Token = { id: string; label: string; grants: Grant[]; createdAt: string; revoked: boolean };
export type Result = { fields: string[]; rows: (string | number | boolean | null)[][] };

export async function api(path: string, method = 'GET', body?: string, signal?: AbortSignal): Promise<Response> {
  const response = await fetch('/api/v1/admin/' + path, {
    method, body, signal, mode: 'same-origin', credentials: 'same-origin', cache: 'no-store',
    referrerPolicy: 'same-origin', referrer: location.origin + '/',
    headers: method === 'GET' ? {} : { 'Content-Type': 'application/json', 'X-MLC-Request': 'console' }
  });
  if (!response.ok) {
    if (response.status === 401 && location.protocol === 'https:') location.replace('/');
    const failure = await response.json().catch(() => null);
    const message = failure?.error?.message;
    if (typeof message === 'string') throw new Error(message);
    if (response.status === 401 || response.status === 403) {
      throw new Error(`Console access was denied (HTTP ${response.status}). Sign in again or ask a workspace owner to check your role.`);
    }
    throw new Error('The gateway returned HTTP ' + response.status + '.');
  }
  return response;
}

export function useList<T>(name: string, refresh: number) {
  const [list, setList] = useState<T[] | null>(null);
  const [error, setError] = useState('');
  useEffect(() => {
    const controller = new AbortController();
    setError('');
    api(name, 'GET', undefined, controller.signal).then(response => response.json()).then((result: Record<string, T[]>) => {
      if (!controller.signal.aborted) setList(result[name] ?? []);
    }).catch(reason => {
      if (!controller.signal.aborted) { setList(null); setError(message(reason)); }
    });
    return () => controller.abort();
  }, [name, refresh]);
  return { list, error };
}

export function message(error: unknown) {
  return error instanceof Error ? error.message : 'The request failed. Try again.';
}

export function lines(input: string) {
  return input.split('\n').map(value => value.trim()).filter(Boolean);
}
