const fs = require('fs');
const path = require('path');

const tsFilePath = path.join(__dirname, '../../front/src/data/novels.ts');
const sqlFilePath = path.join(__dirname, 'seed.sql');

const content = fs.readFileSync(tsFilePath, 'utf8');

// Find the start of the array
const mockNovelsIndex = content.indexOf('MOCK_NOVELS');
if (mockNovelsIndex === -1) {
  console.error("Could not find MOCK_NOVELS in novels.ts");
  process.exit(1);
}
const arrayStart = content.indexOf('[', mockNovelsIndex);
if (arrayStart === -1) {
  console.error("Could not find array start after MOCK_NOVELS in novels.ts");
  process.exit(1);
}

// Extract the array text
let arrayText = content.substring(arrayStart);
// Trim any trailing semicolon or export code at the end if exists
const mockNovels = eval(arrayText);

let sql = `-- Mock Data Seeder for Star Novel\n\n`;

for (const novel of mockNovels) {
  const genresSql = '{' + novel.genres.map(g => `"${g.replace(/"/g, '\\"')}"`).join(',') + '}';
  
  // Parse views (e.g. 7.8M -> 7800000, 2.0M -> 2000000)
  let views = 0;
  if (novel.views.endsWith('M')) {
    views = Math.round(parseFloat(novel.views) * 1000000);
  } else if (novel.views.endsWith('K') || novel.views.endsWith('k')) {
    views = Math.round(parseFloat(novel.views) * 1000);
  } else {
    views = parseInt(novel.views) || 0;
  }

  // Parse words (e.g. 47k -> 47000)
  let words = 0;
  if (novel.words.endsWith('k') || novel.words.endsWith('K')) {
    words = Math.round(parseFloat(novel.words) * 1000);
  } else {
    words = parseInt(novel.words) || 0;
  }

  // Escape synopsis
  const escapedSynopsis = novel.synopsis.replace(/'/g, "''");
  const escapedTitle = novel.title.replace(/'/g, "''");
  const escapedAuthor = novel.author.replace(/'/g, "''");

  sql += `INSERT INTO novels (id, title, author, cover_url, rating, status, synopsis, genres, word_count, view_count) VALUES (\n`;
  sql += `  '${novel.id}',\n`;
  sql += `  '${escapedTitle}',\n`;
  sql += `  '${escapedAuthor}',\n`;
  sql += `  '${novel.cover}',\n`;
  sql += `  ${novel.rating},\n`;
  sql += `  '${novel.status}',\n`;
  sql += `  '${escapedSynopsis}',\n`;
  sql += `  '${genresSql}',\n`;
  sql += `  ${words},\n`;
  sql += `  ${views}\n`;
  sql += `) ON CONFLICT (id) DO UPDATE SET title = EXCLUDED.title, author = EXCLUDED.author, cover_url = EXCLUDED.cover_url, rating = EXCLUDED.rating, status = EXCLUDED.status, synopsis = EXCLUDED.synopsis, genres = EXCLUDED.genres, word_count = EXCLUDED.word_count, view_count = EXCLUDED.view_count;\n\n`;

  for (let idx = 0; idx < novel.chapters.length; idx++) {
    const ch = novel.chapters[idx];
    const escapedChTitle = ch.title.replace(/'/g, "''");
    const escapedChContent = ch.content.replace(/'/g, "''");
    const isPaid = idx >= 2;
    const price = isPaid ? 50 : 0;

    sql += `INSERT INTO chapters (id, novel_id, chapter_index, title, content, word_count, is_paid, price) VALUES (\n`;
    sql += `  '${ch.id}',\n`;
    sql += `  '${novel.id}',\n`;
    sql += `  ${idx},\n`;
    sql += `  '${escapedChTitle}',\n`;
    sql += `  '${escapedChContent}',\n`;
    sql += `  ${ch.wordCount},\n`;
    sql += `  ${isPaid},\n`;
    sql += `  ${price}\n`;
    sql += `) ON CONFLICT (novel_id, chapter_index) DO UPDATE SET title = EXCLUDED.title, content = EXCLUDED.content, word_count = EXCLUDED.word_count, is_paid = EXCLUDED.is_paid, price = EXCLUDED.price;\n\n`;
  }
}

fs.writeFileSync(sqlFilePath, sql, 'utf8');
console.log("Successfully generated seed.sql!");
