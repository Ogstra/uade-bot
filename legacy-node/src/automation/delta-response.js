const DEFAULT_MAX_NODES = 100;
const DEFAULT_MAX_CHARS = 1_000_000;

const failed = (reason) => ({ status: 'failed', reason });

function isValidLimit(value) {
  return Number.isSafeInteger(value) && value >= 0;
}

export function parseDeltaResponse(text, options = {}) {
  if (typeof text !== 'string') {
    return failed('delta_invalid_input');
  }

  const maxNodes = options.maxNodes ?? DEFAULT_MAX_NODES;
  const maxChars = options.maxChars ?? DEFAULT_MAX_CHARS;
  if (!isValidLimit(maxNodes) || !isValidLimit(maxChars)) {
    return failed('delta_invalid_limits');
  }
  if (text.length > maxChars) {
    return failed('delta_too_large');
  }

  const updatePanels = [];
  const hiddenFields = [];
  let cursor = 0;
  let nodeCount = 0;

  const readToken = () => {
    const delimiter = text.indexOf('|', cursor);
    if (delimiter === -1) {
      return null;
    }
    const token = text.slice(cursor, delimiter);
    cursor = delimiter + 1;
    return token;
  };

  while (cursor < text.length) {
    if (nodeCount >= maxNodes) {
      return failed('delta_too_many_nodes');
    }

    const lengthToken = readToken();
    if (lengthToken === null) {
      return failed('delta_missing_delimiter');
    }
    if (!/^\d+$/.test(lengthToken)) {
      return failed('delta_invalid_length');
    }

    const contentLength = Number(lengthToken);
    if (!Number.isSafeInteger(contentLength)) {
      return failed('delta_invalid_length');
    }

    const type = readToken();
    const id = readToken();
    if (type === null || id === null) {
      return failed('delta_missing_delimiter');
    }
    if (contentLength > text.length - cursor) {
      return failed('delta_truncated');
    }

    const contentEnd = cursor + contentLength;
    const content = text.slice(cursor, contentEnd);
    cursor = contentEnd;
    if (text[cursor] !== '|') {
      return failed('delta_missing_delimiter');
    }
    cursor += 1;
    nodeCount += 1;

    if (type === 'error') {
      return failed('delta_error');
    }
    if (type === 'pageRedirect') {
      return failed('delta_redirect');
    }
    if (type === 'updatePanel') {
      updatePanels.push({ id, html: content });
    } else if (type === 'hiddenField') {
      hiddenFields.push({ name: id, value: content });
    }
  }

  return { status: 'parsed', updatePanels, hiddenFields };
}
