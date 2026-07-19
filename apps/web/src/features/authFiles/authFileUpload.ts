import { MAX_AUTH_FILE_SIZE } from '@/utils/constants';
import {
  buildAuthJsonFilePayloads,
  isSub2ApiAuthJsonInput,
  type AuthJsonFilePayload,
} from './sessionAuthConverter';

export type AuthFilePreparationFailure = {
  name: string;
  error: string;
};

export type PreparedAuthFileUpload = {
  files: File[];
  failures: AuthFilePreparationFailure[];
  convertedSourceCount: number;
};

const appendUploadFileNameSuffix = (fileName: string, suffix: number) => {
  const baseName = fileName.toLowerCase().endsWith('.json')
    ? fileName.slice(0, -'.json'.length)
    : fileName;
  return `${baseName}-${suffix}.json`;
};

export const createUniqueConvertedAuthFiles = (
  payloads: AuthJsonFilePayload[],
  reservedFileNames: Iterable<string>
) => {
  const usedNames = new Set(Array.from(reservedFileNames, (name) => name.toLowerCase()));

  return payloads.map((payload) => {
    let fileName = payload.fileName;
    let suffix = 2;
    while (usedNames.has(fileName.toLowerCase())) {
      fileName = appendUploadFileNameSuffix(payload.fileName, suffix);
      suffix += 1;
    }
    usedNames.add(fileName.toLowerCase());
    return new File([JSON.stringify(payload.authJson)], fileName, { type: 'application/json' });
  });
};

export const prepareAuthFilesForUpload = async (files: File[]): Promise<PreparedAuthFileUpload> => {
  const ordinaryFiles: File[] = [];
  const convertedPayloads: AuthJsonFilePayload[] = [];
  const failures: AuthFilePreparationFailure[] = [];
  let convertedSourceCount = 0;

  for (const file of files) {
    let text: string;
    try {
      text = await file.text();
    } catch (err) {
      failures.push({
        name: file.name,
        error: err instanceof Error ? err.message : 'Failed to read file',
      });
      continue;
    }

    if (!isSub2ApiAuthJsonInput(text, MAX_AUTH_FILE_SIZE)) {
      ordinaryFiles.push(file);
      continue;
    }

    try {
      convertedPayloads.push(
        ...buildAuthJsonFilePayloads(
          'sub2api',
          'codex-account.json',
          text,
          new Date(),
          MAX_AUTH_FILE_SIZE
        )
      );
      convertedSourceCount += 1;
    } catch (err) {
      failures.push({
        name: file.name,
        error: err instanceof Error ? err.message : 'Failed to convert sub2api auth JSON',
      });
    }
  }

  const convertedFiles = createUniqueConvertedAuthFiles(
    convertedPayloads,
    ordinaryFiles.map((file) => file.name)
  );
  return {
    files: [...ordinaryFiles, ...convertedFiles],
    failures,
    convertedSourceCount,
  };
};
